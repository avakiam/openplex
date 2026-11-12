package docker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/secrets"
)

// DBNetwork is the internal (no internet) network shared by database servers,
// site containers and Adminer.
const DBNetwork = "openplex_db"

// Adminer is the database GUI, published on loopback and proxied by openplexd.
const (
	AdminerContainer = "opx-adminer"
	AdminerImage     = "adminer:5.4.1"
	AdminerPort      = 7081
)

// Engine describes a database engine.
type Engine struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Image     string `json:"image"`
	Host      string `json:"host"` // hostname reachable from site containers
	Port      int    `json:"port"`
	Shared    bool   `json:"shared"` // one server for all tenants (vs. one container per instance)
	Driver    string `json:"adminer_driver"`
	container string
}

// Engines is the catalog of database engines.
var Engines = map[string]Engine{
	db.EngineMariaDB: {Name: db.EngineMariaDB, Label: "MariaDB 11.8", Image: "mariadb:11.8", Host: "mariadb", Port: 3306,
		Shared: true, Driver: "server", container: "opx-db-mariadb"},
	db.EnginePostgres: {Name: db.EnginePostgres, Label: "PostgreSQL 17", Image: "postgres:17-alpine", Host: "postgres", Port: 5432,
		Shared: true, Driver: "pgsql", container: "opx-db-postgres"},
	db.EngineRedis: {Name: db.EngineRedis, Label: "Redis 7.4", Image: "redis:7.4-alpine", Port: 6379},
}

// RedisContainerName is the container (and hostname) of a Redis instance.
func RedisContainerName(id int64) string { return fmt.Sprintf("opx-redis-%d", id) }

// DatabaseHost returns the hostname site containers use to reach d.
func DatabaseHost(d *db.Database) string {
	if d.Engine == db.EngineRedis {
		return RedisContainerName(d.ID)
	}
	return Engines[d.Engine].Host
}

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,47}$`)

// ValidIdentifier reports whether s is a safe database/user name.
func ValidIdentifier(s string) bool { return identRe.MatchString(s) }

var alnumRe = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)

func (o *Orchestrator) dbDataDir(parts ...string) string {
	return filepath.Join(append([]string{o.cfg.DataDir, "databases"}, parts...)...)
}

// rootPassword returns (creating once) the root password of a shared engine.
func (o *Orchestrator) rootPassword(engine string) (string, error) {
	dir := filepath.Join(o.cfg.DataDir, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, engine+"-root")
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b)), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	pw := secrets.Password(32)
	if err := os.WriteFile(path, []byte(pw+"\n"), 0o600); err != nil {
		return "", err
	}
	return pw, nil
}

// EnsureEngine starts the shared server of engine (creating it on first use)
// and waits until it accepts connections.
func (o *Orchestrator) EnsureEngine(ctx context.Context, engine string) (string, error) {
	e, ok := Engines[engine]
	if !ok || !e.Shared {
		return "", fmt.Errorf("%s is not a shared engine", engine)
	}
	unlock := o.locks.lock(-1 - int64(len(engine))) // engine-wide lock (negative keys never clash with domains)
	defer unlock()
	if err := o.ensureNetwork(ctx, DBNetwork, true); err != nil {
		return "", err
	}
	root, err := o.rootPassword(engine)
	if err != nil {
		return "", err
	}
	c, err := o.findContainer(ctx, e.container)
	if err != nil {
		return "", err
	}
	if c == nil {
		if err := o.ensureImage(ctx, e.Image); err != nil {
			return "", err
		}
		data := o.dbDataDir(engine)
		if err := os.MkdirAll(data, 0o700); err != nil {
			return "", err
		}
		cc := &container.Config{Image: e.Image, Labels: managedLabels("db"), Hostname: e.Host}
		target := "/var/lib/mysql"
		switch engine {
		case db.EngineMariaDB:
			cc.Env = []string{"MARIADB_ROOT_PASSWORD=" + root, "MARIADB_AUTO_UPGRADE=1"}
		case db.EnginePostgres:
			cc.Env = []string{"POSTGRES_PASSWORD=" + root}
			target = "/var/lib/postgresql/data"
		}
		hc := &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Mounts:        []mount.Mount{{Type: mount.TypeBind, Source: data, Target: target}},
			SecurityOpt:   []string{"no-new-privileges:true"},
			ShmSize:       256 << 20,
			LogConfig:     container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		}
		nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{DBNetwork: {Aliases: []string{e.Host}}}}
		created, err := o.cli.ContainerCreate(ctx, cc, hc, nc, nil, e.container)
		if err != nil {
			return "", fmt.Errorf("create %s: %w", e.container, err)
		}
		if err := o.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
			return "", fmt.Errorf("start %s: %w", e.container, err)
		}
		o.log.Info("database engine started", "engine", engine, "image", e.Image)
		c, err = o.findContainer(ctx, e.container)
		if err != nil || c == nil {
			return "", fmt.Errorf("inspect %s after create: %v", e.container, err)
		}
	} else if !c.State.Running {
		if err := o.cli.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
			return "", err
		}
	}
	if err := o.joinTenantNetworks(ctx, e.container, []string{e.Host}); err != nil {
		return "", err
	}
	return c.ID, o.waitReady(ctx, engine, c.ID, root)
}

func (o *Orchestrator) waitReady(ctx context.Context, engine, id, root string) error {
	var cmd []string
	env := []string{}
	switch engine {
	case db.EngineMariaDB:
		// First boot runs init scripts on a temporary socket-only server; require TCP readiness.
		cmd = []string{"mariadb-admin", "ping", "-uroot", "--protocol=tcp", "-h127.0.0.1"}
		env = []string{"MYSQL_PWD=" + root}
	case db.EnginePostgres:
		cmd = []string{"pg_isready", "-U", "postgres", "-h", "127.0.0.1"}
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		out, code, err := o.ExecEnv(ctx, id, cmd, "", env)
		if err == nil && code == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not become ready: %s %v", engine, strings.TrimSpace(out), err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// runSQL executes statements as the engine superuser. Identifiers and
// passwords must be validated by the caller (they are interpolated).
func (o *Orchestrator) runSQL(ctx context.Context, engine string, stmts ...string) (string, error) {
	id, err := o.EnsureEngine(ctx, engine)
	if err != nil {
		return "", err
	}
	var cmd, env []string
	switch engine {
	case db.EngineMariaDB:
		root, err := o.rootPassword(engine)
		if err != nil {
			return "", err
		}
		cmd = []string{"mariadb", "-uroot", "--batch", "--skip-column-names", "-e", strings.Join(stmts, ";\n")}
		env = []string{"MYSQL_PWD=" + root}
	case db.EnginePostgres:
		// Each -c runs in its own transaction (CREATE DATABASE cannot run inside one).
		cmd = []string{"psql", "-U", "postgres", "-v", "ON_ERROR_STOP=1", "-qAt"}
		for _, s := range stmts {
			cmd = append(cmd, "-c", s)
		}
	}
	out, code, err := o.ExecEnv(ctx, id, cmd, "", env)
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, fmt.Errorf("%s: %s", engine, strings.TrimSpace(out))
	}
	return out, nil
}

func checkCreds(d *db.Database, password string) error {
	if !ValidIdentifier(d.Name) || !ValidIdentifier(d.Username) {
		return fmt.Errorf("invalid database or user name")
	}
	if !alnumRe.MatchString(password) {
		return fmt.Errorf("invalid generated password")
	}
	return nil
}

// ProvisionDatabase creates the database and its user (or the Redis instance).
func (o *Orchestrator) ProvisionDatabase(ctx context.Context, d *db.Database, password string) error {
	if err := checkCreds(d, password); err != nil {
		return err
	}
	var err error
	switch d.Engine {
	case db.EngineMariaDB:
		_, err = o.runSQL(ctx, d.Engine,
			fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", d.Name),
			fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'", d.Username, password),
			fmt.Sprintf("ALTER USER '%s'@'%%' IDENTIFIED BY '%s'", d.Username, password),
			// Older versions granted on the unescaped name, where "_" is a
			// wildcard matching other tenants' databases: drop that grant.
			fmt.Sprintf("DELETE FROM mysql.db WHERE User = '%s' AND Host = '%%' AND Db = '%s'", d.Username, d.Name),
			mariaGrant(d),
			"FLUSH PRIVILEGES")
	case db.EnginePostgres:
		_, err = o.runSQL(ctx, d.Engine,
			fmt.Sprintf(`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN CREATE ROLE "%[1]s" LOGIN PASSWORD '%[2]s'; ELSE ALTER ROLE "%[1]s" LOGIN PASSWORD '%[2]s'; END IF; END $$`, d.Username, password))
		if err == nil {
			var exists string
			exists, err = o.runSQL(ctx, d.Engine, fmt.Sprintf(`SELECT 1 FROM pg_database WHERE datname = '%s'`, d.Name))
			if err == nil && strings.TrimSpace(exists) == "" {
				_, err = o.runSQL(ctx, d.Engine, fmt.Sprintf(`CREATE DATABASE "%s" OWNER "%s" ENCODING 'UTF8'`, d.Name, d.Username))
			}
			if err == nil {
				// Other tenants must not even connect to this database, nor to
				// the maintenance databases (idempotent hardening).
				_, err = o.runSQL(ctx, d.Engine,
					fmt.Sprintf(`REVOKE ALL ON DATABASE "%s" FROM PUBLIC`, d.Name),
					`REVOKE CONNECT ON DATABASE postgres FROM PUBLIC`,
					`REVOKE CONNECT ON DATABASE template1 FROM PUBLIC`)
			}
		}
	case db.EngineRedis:
		err = o.deployRedis(ctx, d, password)
	default:
		err = fmt.Errorf("unknown engine %q", d.Engine)
	}
	if err == nil && d.SizeLocked {
		err = o.SetDatabaseReadOnly(ctx, d, true) // the grant above gave everything back
	}
	return err
}

// RotateDatabasePassword sets a new password for the database user.
func (o *Orchestrator) RotateDatabasePassword(ctx context.Context, d *db.Database, password string) error {
	if err := checkCreds(d, password); err != nil {
		return err
	}
	switch d.Engine {
	case db.EngineMariaDB:
		_, err := o.runSQL(ctx, d.Engine, fmt.Sprintf("ALTER USER '%s'@'%%' IDENTIFIED BY '%s'", d.Username, password), "FLUSH PRIVILEGES")
		return err
	case db.EnginePostgres:
		_, err := o.runSQL(ctx, d.Engine, fmt.Sprintf(`ALTER ROLE "%s" PASSWORD '%s'`, d.Username, password))
		return err
	case db.EngineRedis:
		return o.deployRedis(ctx, d, password) // recreated with the new password; data persists
	}
	return fmt.Errorf("unknown engine %q", d.Engine)
}

// DropDatabase removes the database, its user and (for Redis) the instance and data.
func (o *Orchestrator) DropDatabase(ctx context.Context, d *db.Database) error {
	if !ValidIdentifier(d.Name) || !ValidIdentifier(d.Username) {
		return fmt.Errorf("invalid database or user name")
	}
	switch d.Engine {
	case db.EngineMariaDB:
		_, err := o.runSQL(ctx, d.Engine, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", d.Name), fmt.Sprintf("DROP USER IF EXISTS '%s'@'%%'", d.Username))
		return err
	case db.EnginePostgres:
		_, err := o.runSQL(ctx, d.Engine, fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, d.Name), fmt.Sprintf(`DROP ROLE IF EXISTS "%s"`, d.Username))
		return err
	case db.EngineRedis:
		if c, err := o.findContainer(ctx, RedisContainerName(d.ID)); err != nil {
			return err
		} else if c != nil {
			if err := o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				return err
			}
		}
		return os.RemoveAll(o.dbDataDir("redis", strconv.FormatInt(d.ID, 10)))
	}
	return fmt.Errorf("unknown engine %q", d.Engine)
}

const redisUID, redisGID = 999, 1000 // the "redis" user of the official alpine image

func (o *Orchestrator) deployRedis(ctx context.Context, d *db.Database, password string) error {
	unlock := o.locks.lock(-1000 - d.ID)
	defer unlock()
	e := Engines[db.EngineRedis]
	tenantNet, err := o.ensureTenantNetwork(ctx, d.OwnerID)
	if err != nil {
		return err
	}
	if err := o.ensureImage(ctx, e.Image); err != nil {
		return err
	}
	data := o.dbDataDir("redis", strconv.FormatInt(d.ID, 10))
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	_ = os.Chown(data, redisUID, redisGID)
	name := RedisContainerName(d.ID)
	if c, err := o.findContainer(ctx, name); err != nil {
		return err
	} else if c != nil {
		if err := o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			return err
		}
	}
	labels := managedLabels("redis")
	labels["openplex.database_id"] = strconv.FormatInt(d.ID, 10)
	pids := int64(128)
	cc := &container.Config{
		Image: e.Image, Labels: labels, Hostname: name, User: fmt.Sprintf("%d:%d", redisUID, redisGID),
		Env: []string{"REDIS_PASSWORD=" + password},
		Cmd: []string{"sh", "-c", `exec redis-server --requirepass "$REDIS_PASSWORD" --appendonly yes --maxmemory 96mb --maxmemory-policy allkeys-lru --dir /data`},
	}
	hc := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		Mounts:        []mount.Mount{{Type: mount.TypeBind, Source: data, Target: "/data"}},
		Resources:     container.Resources{Memory: 128 << 20, NanoCPUs: 5e8, PidsLimit: &pids},
		CapDrop:       []string{"ALL"},
		SecurityOpt:   []string{"no-new-privileges:true"},
		LogConfig:     container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "5m", "max-file": "2"}},
	}
	nc := &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{tenantNet: {Aliases: []string{name}}}}
	created, err := o.cli.ContainerCreate(ctx, cc, hc, nc, nil, name)
	if err != nil {
		return fmt.Errorf("create redis: %w", err)
	}
	if err := o.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start redis: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, code, err := o.ExecEnv(ctx, created.ID, []string{"sh", "-c", `redis-cli --no-auth-warning -a "$REDIS_PASSWORD" ping`}, "", nil)
		if err == nil && code == 0 && strings.Contains(out, "PONG") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("redis did not become ready: %s", strings.TrimSpace(out))
		}
		time.Sleep(time.Second)
	}
}

// DatabaseSizes returns the on-disk (or in-memory, for Redis) size of each database.
func (o *Orchestrator) DatabaseSizes(ctx context.Context, dbs []*db.Database) map[int64]int64 {
	out := map[int64]int64{}
	byEngine := map[string]map[string]int64{}
	for _, eng := range []string{db.EngineMariaDB, db.EnginePostgres} {
		has := false
		for _, d := range dbs {
			has = has || d.Engine == eng
		}
		if !has {
			continue
		}
		c, err := o.findContainer(ctx, Engines[eng].container)
		if err != nil || c == nil || !c.State.Running {
			continue
		}
		q := "SELECT table_schema, COALESCE(SUM(data_length+index_length),0) FROM information_schema.tables GROUP BY table_schema"
		if eng == db.EnginePostgres {
			q = "SELECT datname || chr(9) || pg_database_size(datname) FROM pg_database"
		}
		res, err := o.runSQL(ctx, eng, q)
		if err != nil {
			continue
		}
		m := map[string]int64{}
		for _, line := range strings.Split(res, "\n") {
			f := strings.Split(strings.TrimSpace(line), "\t")
			if len(f) == 2 {
				n, _ := strconv.ParseInt(f[1], 10, 64)
				m[f[0]] = n
			}
		}
		byEngine[eng] = m
	}
	for _, d := range dbs {
		if d.Engine == db.EngineRedis {
			if c, err := o.findContainer(ctx, RedisContainerName(d.ID)); err == nil && c != nil && c.State.Running {
				res, code, err := o.Exec(ctx, c.ID, []string{"sh", "-c", `redis-cli --no-auth-warning -a "$REDIS_PASSWORD" info memory | grep '^used_memory:'`}, "")
				if err == nil && code == 0 {
					n, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(res), "used_memory:")), 10, 64)
					out[d.ID] = n
				}
			}
			continue
		}
		if m, ok := byEngine[d.Engine]; ok {
			out[d.ID] = m[d.Name]
		}
	}
	return out
}

// EnsureAdminer runs the Adminer GUI on 127.0.0.1:AdminerPort, attached to the
// database network so it can reach the servers by hostname.
func (o *Orchestrator) EnsureAdminer(ctx context.Context) error {
	if err := o.ensureNetwork(ctx, DBNetwork, true); err != nil {
		return err
	}
	c, err := o.findContainer(ctx, AdminerContainer)
	if err != nil {
		return err
	}
	if c != nil && c.Config.Image == AdminerImage {
		if !c.State.Running {
			return o.cli.ContainerStart(ctx, c.ID, container.StartOptions{})
		}
		return nil
	}
	if c != nil {
		if err := o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			return err
		}
	}
	if err := o.ensureImage(ctx, AdminerImage); err != nil {
		return err
	}
	cc := &container.Config{
		Image: AdminerImage, Labels: managedLabels("tool"),
		ExposedPorts: nat.PortSet{"8080/tcp": {}},
		Env:          []string{"ADMINER_DEFAULT_SERVER=mariadb"},
	}
	hc := &container.HostConfig{
		PortBindings:  nat.PortMap{"8080/tcp": {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(AdminerPort)}}},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		CapDrop:       []string{"ALL"},
		SecurityOpt:   []string{"no-new-privileges:true"},
		Resources:     container.Resources{Memory: 256 << 20},
	}
	created, err := o.cli.ContainerCreate(ctx, cc, hc, nil, nil, AdminerContainer)
	if err != nil {
		return fmt.Errorf("create adminer: %w", err)
	}
	if err := o.cli.NetworkConnect(ctx, DBNetwork, created.ID, &network.EndpointSettings{}); err != nil {
		return fmt.Errorf("connect adminer: %w", err)
	}
	return o.cli.ContainerStart(ctx, created.ID, container.StartOptions{})
}

// ReconcileDatabases makes sure engines and Redis instances in the DB exist,
// and removes orphaned Redis containers.
func (o *Orchestrator) ReconcileDatabases(ctx context.Context, box *secrets.Box) error {
	dbs, err := o.db.ListDatabases(ctx, nil)
	if err != nil {
		return err
	}
	var errs []error
	engines := map[string]bool{}
	want := map[string]bool{}
	for _, d := range dbs {
		if d.Engine == db.EngineRedis {
			want[RedisContainerName(d.ID)] = true
			c, err := o.findContainer(ctx, RedisContainerName(d.ID))
			moved := c != nil && c.NetworkSettings != nil && c.NetworkSettings.Networks[TenantNetwork(d.OwnerID)] == nil // from before account networks
			if err == nil && (c == nil || moved) && d.Status == db.StatusRunning {
				pw, err := box.Open(d.PasswordEnc)
				if err == nil {
					err = o.deployRedis(ctx, d, pw)
				}
				if err != nil {
					errs = append(errs, fmt.Errorf("redis %s: %w", d.Name, err))
				}
			}
			continue
		}
		engines[d.Engine] = true
	}
	ready := map[string]bool{}
	for eng := range engines {
		if _, err := o.EnsureEngine(ctx, eng); err != nil {
			errs = append(errs, err)
		} else {
			ready[eng] = true
		}
	}
	// A fresh engine (new server, lost volume) has none of the databases and
	// users the panel knows about; creating them is idempotent.
	for _, d := range dbs {
		if d.Engine == db.EngineRedis || d.Status != db.StatusRunning || !ready[d.Engine] {
			continue
		}
		pw, err := box.Open(d.PasswordEnc)
		if err == nil {
			err = o.ProvisionDatabase(ctx, d, pw)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("database %s: %w", d.Name, err))
		}
	}
	if len(dbs) > 0 {
		if err := o.EnsureAdminer(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	existing, err := o.listManaged(ctx, "redis")
	if err != nil {
		return err
	}
	for _, c := range existing {
		if len(c.Names) > 0 && !want[strings.TrimPrefix(c.Names[0], "/")] {
			_ = o.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
		}
	}
	return errors.Join(errs...)
}

// ResetMariaDB drops and recreates an (empty) MariaDB database before a
// restore; the user's grants on it are kept.
func (o *Orchestrator) ResetMariaDB(ctx context.Context, d *db.Database) error {
	if !ValidIdentifier(d.Name) || !ValidIdentifier(d.Username) {
		return fmt.Errorf("invalid database or user name")
	}
	_, err := o.runSQL(ctx, db.EngineMariaDB,
		fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", d.Name),
		fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", d.Name),
		mariaGrant(d))
	return err
}

// mariaGrant gives d's user all privileges on exactly d's database. In a
// database-level GRANT "_" and "%" are wildcards, so they are escaped:
// granting on alice_wp unescaped would also match alicexwp, alice1wp...
func mariaGrant(d *db.Database) string {
	name := strings.NewReplacer("_", `\_`, "%", `\%`).Replace(d.Name)
	return fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%'", name, d.Username)
}

// EngineAddr returns ip:port of a shared engine's server on the internal
// database network, reachable from the host (remote database access).
func (o *Orchestrator) EngineAddr(ctx context.Context, engine string) (string, error) {
	e, ok := Engines[engine]
	if !ok || !e.Shared {
		return "", fmt.Errorf("%s is not a shared engine", engine)
	}
	c, err := o.findContainer(ctx, e.container)
	if err != nil {
		return "", err
	}
	if c == nil || !c.State.Running {
		return "", fmt.Errorf("the %s server is not running", engine)
	}
	ep := c.NetworkSettings.Networks[DBNetwork]
	if ep == nil || ep.IPAddress == "" {
		return "", fmt.Errorf("the %s server has no address", engine)
	}
	return net.JoinHostPort(ep.IPAddress, strconv.Itoa(e.Port)), nil
}

// SetDatabaseReadOnly makes a MariaDB/PostgreSQL database read-only for its
// user (over its size limit) or gives the full privileges back, and closes the
// user's open connections so the change applies at once.
//
// MariaDB keeps SELECT, DELETE, DROP, LOCK TABLES and SHOW VIEW, so the
// owner can still free space. PostgreSQL has no privilege the owner of a
// database can lose: new sessions start read-only (default_transaction_read_only),
// which stops applications but not someone who turns it off on purpose.
func (o *Orchestrator) SetDatabaseReadOnly(ctx context.Context, d *db.Database, ro bool) error {
	if !ValidIdentifier(d.Name) || !ValidIdentifier(d.Username) {
		return fmt.Errorf("invalid database or user name")
	}
	switch d.Engine {
	case db.EngineMariaDB:
		name := strings.NewReplacer("_", `\_`, "%", `\%`).Replace(d.Name)
		grant := mariaGrant(d)
		if ro {
			grant = fmt.Sprintf("GRANT SELECT, DELETE, DROP, LOCK TABLES, SHOW VIEW ON `%s`.* TO '%s'@'%%'", name, d.Username)
		}
		// REVOKE fails when nothing is granted; grant something first.
		if _, err := o.runSQL(ctx, d.Engine,
			fmt.Sprintf("GRANT USAGE ON `%s`.* TO '%s'@'%%'", name, d.Username),
			fmt.Sprintf("REVOKE ALL PRIVILEGES ON `%s`.* FROM '%s'@'%%'", name, d.Username),
			grant, "FLUSH PRIVILEGES"); err != nil {
			return err
		}
		ids, err := o.runSQL(ctx, d.Engine, fmt.Sprintf("SELECT id FROM information_schema.processlist WHERE user = '%s'", d.Username))
		if err != nil {
			return err
		}
		var kills []string
		for _, id := range strings.Fields(ids) {
			if _, err := strconv.ParseInt(id, 10, 64); err == nil {
				kills = append(kills, "KILL CONNECTION "+id)
			}
		}
		if len(kills) > 0 {
			_, _ = o.runSQL(ctx, d.Engine, kills...) // a connection may close by itself meanwhile
		}
		return nil
	case db.EnginePostgres:
		set := fmt.Sprintf(`ALTER ROLE "%s" IN DATABASE "%s" RESET default_transaction_read_only`, d.Username, d.Name)
		if ro {
			set = fmt.Sprintf(`ALTER ROLE "%s" IN DATABASE "%s" SET default_transaction_read_only = on`, d.Username, d.Name)
		}
		_, err := o.runSQL(ctx, d.Engine, set,
			fmt.Sprintf(`SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE usename = '%s' AND datname = '%s'`, d.Username, d.Name))
		return err
	}
	return fmt.Errorf("%s databases have no size limit", d.Engine)
}

// AnalyzeDatabase refreshes the table statistics of a MariaDB database, so
// that its measured size drops once rows are deleted.
func (o *Orchestrator) AnalyzeDatabase(ctx context.Context, d *db.Database) error {
	if d.Engine != db.EngineMariaDB || !ValidIdentifier(d.Name) {
		return nil
	}
	res, err := o.runSQL(ctx, d.Engine, fmt.Sprintf("SELECT table_name FROM information_schema.tables WHERE table_schema = '%s' AND table_type = 'BASE TABLE'", d.Name))
	if err != nil {
		return err
	}
	var tables []string
	for _, t := range strings.Split(strings.TrimRight(res, "\n"), "\n") {
		if t != "" {
			// Table names are the tenant's: quote them (a backtick is doubled).
			tables = append(tables, fmt.Sprintf("`%s`.`%s`", d.Name, strings.ReplaceAll(t, "`", "``")))
		}
	}
	for len(tables) > 0 {
		n := min(len(tables), 200)
		if _, err := o.runSQL(ctx, d.Engine, "ANALYZE TABLE "+strings.Join(tables[:n], ", ")); err != nil {
			return err
		}
		tables = tables[n:]
	}
	return nil
}
