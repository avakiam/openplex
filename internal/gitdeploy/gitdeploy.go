// Package gitdeploy validates the settings of Git deploys and creates their
// deploy keys. The deploy itself runs in a container (see docker.RunGit).
package gitdeploy

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

var (
	// git@github.com:owner/repo.git
	scpRe = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._~/-]+$`)
	// a host name or address
	hostRe   = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	pathRe   = regexp.MustCompile(`^/[A-Za-z0-9._~/%-]+$`)
	branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)
)

// ValidRepo checks a repository address: SSH ("git@host:owner/repo.git" or
// "ssh://git@host[:port]/path") or HTTPS without credentials in it. Local
// paths and other transports are refused.
func ValidRepo(u string) error {
	switch {
	case u == "" || len(u) > 500 || strings.HasPrefix(u, "-") || strings.ContainsAny(u, " \t\r\n\\\"'`$;|&<>"):
		return errors.New("the repository must be an SSH or HTTPS address such as git@github.com:owner/repo.git")
	case scpRe.MatchString(u):
		if strings.Contains(u, "..") {
			return errors.New("the repository address is not valid")
		}
		return nil
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "ssh") || !hostRe.MatchString(p.Hostname()) ||
		strings.HasPrefix(p.Hostname(), "-") || !pathRe.MatchString(p.Path) || strings.Contains(p.Path, "..") ||
		p.RawQuery != "" || p.Fragment != "" {
		return errors.New("the repository must be an SSH or HTTPS address such as git@github.com:owner/repo.git")
	}
	if _, has := p.User.Password(); has || (p.Scheme == "https" && p.User != nil) {
		return errors.New("don't put a password or token in the address: use the deploy key with an SSH address")
	}
	return nil
}

// ValidBranch checks a branch name.
func ValidBranch(b string) error {
	if !branchRe.MatchString(b) || strings.HasPrefix(b, "-") || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") ||
		strings.Contains(b, "..") || strings.Contains(b, "//") || strings.HasSuffix(b, ".lock") {
		return errors.New("the branch name is not valid")
	}
	return nil
}

// CleanTarget normalizes the folder the repository goes into, relative to
// the website's directory ("" is the directory itself). The logs folder is
// refused.
func CleanTarget(t string) (string, error) {
	t = strings.TrimSpace(strings.ReplaceAll(t, "\\", "/"))
	c := strings.Trim(path.Clean("/"+t), "/")
	if strings.Contains(t, "..") || len(c) > 200 || strings.ContainsAny(c, "\x00\r\n") {
		return "", errors.New("the folder must be inside the website's directory")
	}
	if c == "logs" || strings.HasPrefix(c, "logs/") {
		return "", errors.New("the logs folder cannot hold the repository")
	}
	return c, nil
}

// ValidBuild checks a build command.
func ValidBuild(cmd string) error {
	if len(cmd) > 2000 || strings.ContainsRune(cmd, 0) {
		return errors.New("the build command is too long")
	}
	return nil
}

// NewKey creates an Ed25519 deploy key: the private key (OpenSSH PEM) and the
// public key line to add to the repository.
func NewKey(comment string) (private, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + comment, nil
}

// Result reads the commit a deploy ended on from the git job's output.
func Result(output string) (commit, subject string) {
	for _, l := range strings.Split(output, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "OPX_COMMIT="); ok && len(v) <= 64 {
			commit = v
		} else if v, ok := strings.CutPrefix(l, "OPX_SUBJECT="); ok {
			subject = v
		}
	}
	return commit, subject
}

// PushedBranch reads the branch a push webhook is about (GitHub, GitLab,
// Gitea, Forgejo and Bitbucket send "ref": "refs/heads/<branch>"); "" when
// the payload does not say.
func PushedBranch(ref string) string {
	b, _ := strings.CutPrefix(ref, "refs/heads/")
	if b == ref {
		return ""
	}
	return b
}
