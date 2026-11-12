package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/mount"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/ssl"
)

// gitScript fetches one branch of a repository into a folder of the
// website and makes the folder's tracked files match it. Files the
// repository does not track (uploads, caches, .env) are left alone.
const gitScript = `set -eu
mkdir -p "$OPX_TARGET"
cd "$OPX_TARGET"
if [ ! -d .git ]; then
	git init -q
	echo "==> created a repository in $OPX_TARGET"
fi
git config --unset-all remote.origin.url 2>/dev/null || true
git config remote.origin.url "$OPX_REPO"
echo "==> fetching $OPX_BRANCH from $OPX_REPO"
git -c protocol.file.allow=never fetch --depth 1 --no-tags origin "refs/heads/$OPX_BRANCH"
git -c advice.detachedHead=false checkout -q --force FETCH_HEAD
if [ -f .gitmodules ]; then
	echo "==> updating submodules"
	git -c protocol.file.allow=never submodule update --init --recursive --depth 1
fi
echo "OPX_COMMIT=$(git rev-parse HEAD)"
echo "OPX_SUBJECT=$(git log -1 --format=%s | head -c 200)"
`

// GitDir is where the panel keeps a website's deploy key and known hosts,
// mounted in the git job.
func (o *Orchestrator) GitDir(domainID int64) string {
	return filepath.Join(o.cfg.DataDir, "git", fmt.Sprint(domainID))
}

// RunGit fetches branch of repo into target (relative to the website's
// folder) in a throwaway container running as the website's user: it sees
// only the website's folder and GitDir (key "key", known hosts accepted on
// first use). Output goes to w; it returns the exit code.
func (o *Orchestrator) RunGit(ctx context.Context, d *db.Domain, repo, branch, target string, w io.Writer) (int, error) {
	if err := o.EnsureShellImage(ctx); err != nil {
		return -1, err
	}
	site := path.Join(ssl.EdgeSitesRoot, d.Name)
	uid := SiteUID(d.ID)
	// ssh refuses to run for a uid without a passwd entry.
	accounts := map[string]string{
		"passwd": fmt.Sprintf("root:x:0:0:root:/root:/bin/sh\nsite:x:%d:%d:website:/tmp:/bin/sh\n", uid, uid),
		"group":  fmt.Sprintf("root:x:0:\nsite:x:%d:\n", uid),
	}
	for name, content := range accounts {
		if err := os.WriteFile(filepath.Join(o.GitDir(d.ID), name), []byte(content), 0o644); err != nil {
			return -1, err
		}
	}
	// A host that does not answer, or stops answering, fails the deploy
	// instead of holding it until the deploy times out.
	ssh := "ssh -i /opx-git/key -o IdentitiesOnly=yes -o UserKnownHostsFile=/opx-git/known_hosts -o StrictHostKeyChecking=accept-new" +
		" -o BatchMode=yes -o ConnectTimeout=30 -o ServerAliveInterval=15 -o ServerAliveCountMax=4"
	res, err := o.RunJob(ctx, Job{
		Image: ShellImage(),
		Cmd:   []string{"sh", "-c", gitScript},
		Env: []string{"HOME=/tmp", "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=" + ssh,
			"GIT_HTTP_LOW_SPEED_LIMIT=1000", "GIT_HTTP_LOW_SPEED_TIME=60",
			// The container sees only this website's files: git's ownership
			// check would only refuse files a restore or upload left with
			// another owner.
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=safe.directory", "GIT_CONFIG_VALUE_0=*",
			"OPX_REPO=" + repo, "OPX_BRANCH=" + branch, "OPX_TARGET=" + strings.TrimSuffix(path.Join(site, target), "/")},
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: o.SiteDir(d.Name), Target: site},
			{Type: mount.TypeBind, Source: o.GitDir(d.ID), Target: "/opx-git"},
			{Type: mount.TypeBind, Source: filepath.Join(o.GitDir(d.ID), "passwd"), Target: "/etc/passwd", ReadOnly: true},
			{Type: mount.TypeBind, Source: filepath.Join(o.GitDir(d.ID), "group"), Target: "/etc/group", ReadOnly: true},
		},
		User:     fmt.Sprintf("%d:%d", uid, uid),
		MemoryMB: 768,
	})
	if err != nil {
		return -1, err
	}
	_, _ = io.WriteString(w, res.Stdout)
	_, _ = io.WriteString(w, res.Stderr)
	return res.Exit, nil
}

// RunSiteBuild runs a build command for website d in the folder dir
// (absolute, inside the container): in the website's own container, or in a
// throwaway shell container for static websites.
func (o *Orchestrator) RunSiteBuild(ctx context.Context, d *db.Domain, dir, command string, w io.Writer) (int, error) {
	return o.runInSite(ctx, d, `cd "$OPX_BUILD_DIR" && `+command, []string{"OPX_BUILD_DIR=" + dir}, w)
}
