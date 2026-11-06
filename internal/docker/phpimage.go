package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/pkg/jsonmessage"
)

//go:embed php/Dockerfile php/openplex.ini
var phpRecipe embed.FS

// phpRecipeHash identifies the build recipe; changing the Dockerfile or ini
// yields a new image tag, so sites are rebuilt and redeployed automatically.
var phpRecipeHash = func() string {
	h := sha256.New()
	_ = fs.WalkDir(phpRecipe, "php", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := phpRecipe.ReadFile(p)
		h.Write([]byte(p))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:10]
}()

// PHPImage is the locally built runtime image for a PHP version.
func PHPImage(version string) string {
	return fmt.Sprintf("openplex/php:%s-%s", version, phpRecipeHash)
}

func phpBuildContext() (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range []string{"Dockerfile", "openplex.ini"} {
		b, err := phpRecipe.ReadFile("php/" + name)
		if err != nil {
			return nil, err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), ModTime: time.Unix(0, 0)}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(b); err != nil {
			return nil, err
		}
	}
	return &buf, tw.Close()
}

// ensurePHPImage builds openplex/php:<version>-<hash> if it is not present.
// The first build of a version compiles the extensions and takes a few minutes.
func (o *Orchestrator) ensurePHPImage(ctx context.Context, version string) error {
	tag := PHPImage(version)
	o.buildMu.Lock() // one compile at a time: builds are CPU-heavy
	defer o.buildMu.Unlock()
	if _, err := o.cli.ImageInspect(ctx, tag); err == nil {
		return nil
	}
	if err := o.ensureImage(ctx, fmt.Sprintf("php:%s-fpm-alpine", version)); err != nil {
		return err
	}
	bctx, err := phpBuildContext()
	if err != nil {
		return err
	}
	o.log.Info("building PHP runtime image (first use of this version, takes a few minutes)", "image", tag)
	start := time.Now()
	v := version
	resp, err := o.cli.ImageBuild(ctx, bctx, build.ImageBuildOptions{
		Tags:        []string{tag},
		Dockerfile:  "Dockerfile",
		BuildArgs:   map[string]*string{"PHP_VERSION": &v},
		Remove:      true,
		ForceRemove: true,
		Labels:      map[string]string{LabelManaged: "true", LabelKind: "image"},
	})
	if err != nil {
		return fmt.Errorf("build %s: %w", tag, err)
	}
	defer resp.Body.Close()
	var tail bytes.Buffer
	if err := jsonmessage.DisplayJSONMessagesStream(resp.Body, &tailWriter{buf: &tail, max: 4096}, 0, false, nil); err != nil {
		return fmt.Errorf("build %s: %w\n%s", tag, err, tail.String())
	}
	if _, err := o.cli.ImageInspect(ctx, tag); err != nil {
		return fmt.Errorf("build %s finished but the image is missing:\n%s", tag, tail.String())
	}
	o.log.Info("PHP runtime image built", "image", tag, "took", time.Since(start).Round(time.Second))
	o.removeStalePHPImages(ctx, version)
	return nil
}

// removeStalePHPImages deletes images of the same PHP version built from an
// older recipe. Images still used by a container are kept (removal fails).
func (o *Orchestrator) removeStalePHPImages(ctx context.Context, version string) {
	imgs, err := o.cli.ImageList(ctx, image.ListOptions{Filters: filters.NewArgs(filters.Arg("reference", "openplex/php"))})
	if err != nil {
		return
	}
	current := PHPImage(version)
	prefix := "openplex/php:" + version + "-"
	for _, im := range imgs {
		for _, t := range im.RepoTags {
			if strings.HasPrefix(t, prefix) && t != current {
				if _, err := o.cli.ImageRemove(ctx, t, image.RemoveOptions{PruneChildren: true}); err == nil {
					o.log.Info("removed stale PHP image", "image", t)
				}
			}
		}
	}
}

// tailWriter keeps only the last max bytes written (build log for errors).
type tailWriter struct {
	buf *bytes.Buffer
	max int
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if t.buf.Len() > t.max {
		b := t.buf.Bytes()[t.buf.Len()-t.max:]
		nb := append([]byte(nil), b...)
		t.buf.Reset()
		t.buf.Write(nb)
	}
	return len(p), nil
}
