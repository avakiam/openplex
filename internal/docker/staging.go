package docker

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/docker/docker/api/types/mount"

	"openplex.dev/openplex/internal/db"
)

// CopySiteFiles makes the document root of website to a copy of the one of
// website from (rsync --delete), except the paths in keep (relative to the
// document root; they are neither copied nor deleted). The copy runs in a
// throwaway container without network that sees only the two document roots;
// links are copied as links, setuid/setgid bits are dropped and every file
// ends up owned by to's user.
func (o *Orchestrator) CopySiteFiles(ctx context.Context, from, to *db.Domain, keep []string) error {
	if err := o.EnsureShellImage(ctx); err != nil {
		return err
	}
	args := []string{"rsync", "-rlpt", "--delete", "--chmod=ug-s", "--chown=" + fmt.Sprintf("%d:%d", SiteUID(to.ID), SiteUID(to.ID))}
	for _, k := range keep {
		k = strings.Trim(path.Clean("/"+k), "/")
		if k == "" || strings.ContainsAny(k, "*?[\\") {
			return fmt.Errorf("invalid path to keep %q", k)
		}
		args = append(args, "--exclude=/"+k)
	}
	args = append(args, "/from/", "/to/")
	res, err := o.RunJob(ctx, Job{
		Image: ShellImage(),
		Cmd:   args,
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: path.Join(o.SiteDir(from.Name), from.DocumentRoot), Target: "/from", ReadOnly: true},
			{Type: mount.TypeBind, Source: path.Join(o.SiteDir(to.Name), to.DocumentRoot), Target: "/to"},
		},
		Isolated: true,
	})
	if err != nil {
		return fmt.Errorf("copying the files: %w", err)
	}
	if res.Exit != 0 {
		return fmt.Errorf("copying the files failed (exit %d): %s", res.Exit, strings.TrimSpace(lastN(res.Stderr, 500)))
	}
	return nil
}

// copyMariaDBScript dumps database $1 into database $2. DEFINER clauses
// (views, triggers, routines) would name the source's user: they are dropped,
// so the objects belong to whoever runs them.
const copyMariaDBScript = `set -o pipefail
mariadb-dump -uroot --single-transaction --quick --routines --triggers --hex-blob "$1" |
	sed -E 's/DEFINER=` + "`[^`]*`@`[^`]*`" + `//g' |
	mariadb -uroot "$2"`

// CopyMariaDB replaces the contents of database to with those of from.
func (o *Orchestrator) CopyMariaDB(ctx context.Context, from, to *db.Database) error {
	if from.Engine != db.EngineMariaDB || to.Engine != db.EngineMariaDB {
		return fmt.Errorf("only MariaDB databases can be copied")
	}
	if !ValidIdentifier(from.Name) || !ValidIdentifier(to.Name) {
		return fmt.Errorf("invalid database name")
	}
	if err := o.ResetMariaDB(ctx, to); err != nil {
		return err
	}
	cid, err := o.EngineContainer(ctx, db.EngineMariaDB)
	if err != nil {
		return err
	}
	env, err := o.RootEnv(db.EngineMariaDB)
	if err != nil {
		return err
	}
	if _, err := o.ExecToWriter(ctx, cid, []string{"bash", "-c", copyMariaDBScript, "copy", from.Name, to.Name}, env, io.Discard); err != nil {
		return fmt.Errorf("copying the database: %w", err)
	}
	return nil
}
