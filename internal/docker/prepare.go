package docker

import (
	"context"
	"fmt"
	"slices"

	"openplex.dev/openplex/internal/db"
)

// BaseImages are the images every server ends up using; Mail and DNS ones
// only once those services are turned on.
func BaseImages(mail bool) []string {
	out := []string{EdgeImage, Engines[db.EngineMariaDB].Image, Engines[db.EnginePostgres].Image, Engines[db.EngineRedis].Image,
		AdminerImage, DNSImage}
	if mail {
		out = append(out, MailImage, WebmailImage)
	}
	return out
}

// PrepareImages downloads the images a server uses and builds its own ones
// (the static and shell images, and PHP for the versions given), so that a
// server made from a template does not wait for them on first use. extra are
// images of other packages (the backup tool). It needs no OpenPlex state:
// images are all it touches.
func (o *Orchestrator) PrepareImages(ctx context.Context, php []string, mail bool, extra ...string) error {
	versions := Runtimes["php"].Versions
	for _, v := range php {
		if !slices.Contains(versions, v) {
			return fmt.Errorf("unknown PHP version %s (available: %v)", v, versions)
		}
	}
	for _, ref := range append(BaseImages(mail), extra...) {
		if err := o.ensureImage(ctx, ref); err != nil {
			return err
		}
	}
	if err := o.EnsureStaticImage(ctx); err != nil {
		return err
	}
	if err := o.EnsureShellImage(ctx); err != nil {
		return err
	}
	for _, v := range php {
		if err := o.ensurePHPImage(ctx, v); err != nil {
			return err
		}
	}
	return nil
}
