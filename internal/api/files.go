package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"openplex.dev/openplex/internal/db"
	"openplex.dev/openplex/internal/docker"
	"openplex.dev/openplex/internal/files"
)

const maxUploadBytes = 2 << 30 // 2 GiB per upload request

// jailFor resolves {id} to the site's file jail.
func (s *Server) jailFor(w http.ResponseWriter, r *http.Request) (*db.Domain, *files.Jail) {
	d := s.loadDomain(w, r)
	if d == nil {
		return nil, nil
	}
	if d.Kind == db.KindAlias {
		badRequest(w, "aliases have no files; open the parent website")
		return nil, nil
	}
	uid := docker.SiteUID(d.ID)
	return d, &files.Jail{Root: filepath.Join(s.cfg.SitesDir(), d.Name), UID: uid, GID: uid}
}

func (s *Server) fileErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, files.ErrTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
	case errors.Is(err, files.ErrRoot):
		badRequest(w, err.Error())
	default:
		if strings.Contains(err.Error(), "escapes from parent") {
			badRequest(w, "path escapes the site directory")
			return
		}
		s.fail(w, r, err)
	}
}

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	_, j := s.jailFor(w, r)
	if j == nil {
		return
	}
	ents, err := j.List(r.URL.Query().Get("path"))
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": files.Clean(r.URL.Query().Get("path")), "entries": ents})
}

func (s *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	_, j := s.jailFor(w, r)
	if j == nil {
		return
	}
	p := r.URL.Query().Get("path")
	b, err := j.ReadFile(p)
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	if !utf8.Valid(b) {
		writeErr(w, http.StatusUnsupportedMediaType, "binary", "binary files cannot be opened in the editor; download it instead")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": files.Clean(p), "content": string(b), "size": len(b)})
}

func (s *Server) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	d, j := s.jailFor(w, r)
	if j == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, files.MaxEditSize+(64<<10))
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if !decodeLarge(w, r, &in) {
		return
	}
	n, err := j.WriteFile(in.Path, strings.NewReader(in.Content))
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	s.audit(r, "file.write", d.Name, files.Clean(in.Path))
	writeJSON(w, http.StatusOK, map[string]any{"path": files.Clean(in.Path), "size": n})
}

func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	_, j := s.jailFor(w, r)
	if j == nil {
		return
	}
	f, st, err := j.Open(r.URL.Query().Get("path"))
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": st.Name()}))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	d, j := s.jailFor(w, r)
	if j == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		badRequest(w, "expected multipart/form-data")
		return
	}
	dir := files.Clean(r.URL.Query().Get("path"))
	var saved []string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			badRequest(w, "malformed upload: "+err.Error())
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		name := path.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
		if name == "." || name == "/" || name == ".." {
			part.Close()
			continue
		}
		target := path.Join(dir, name)
		_, err = j.WriteFile(target, part)
		part.Close()
		if err != nil {
			s.fileErr(w, r, err)
			return
		}
		saved = append(saved, files.Clean(target))
	}
	s.audit(r, "file.upload", d.Name, fmt.Sprintf("%d files to %s", len(saved), dir))
	writeJSON(w, http.StatusCreated, map[string]any{"saved": saved})
}

func (s *Server) handleFileOp(w http.ResponseWriter, r *http.Request) {
	d, j := s.jailFor(w, r)
	if j == nil {
		return
	}
	op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	var in struct {
		Path  string   `json:"path"`
		Paths []string `json:"paths"`
		From  string   `json:"from"`
		To    string   `json:"to"`
		Mode  string   `json:"mode"`
		Dest  string   `json:"dest"`
	}
	if !decode(w, r, &in) {
		return
	}
	var err error
	result := map[string]any{}
	switch op {
	case "mkdir":
		err = j.Mkdir(in.Path)
	case "rename":
		err = j.Rename(in.From, in.To)
	case "delete":
		if len(in.Paths) == 0 && in.Path != "" {
			in.Paths = []string{in.Path}
		}
		for _, p := range in.Paths {
			if err = j.Remove(p); err != nil {
				break
			}
		}
	case "chmod":
		mode, perr := strconv.ParseUint(in.Mode, 8, 32)
		if perr != nil {
			badRequest(w, "mode must be octal, e.g. 0644")
			return
		}
		err = j.Chmod(in.Path, uint32(mode))
	case "extract":
		dest := in.Dest
		if dest == "" {
			dest = path.Dir(files.Clean(in.Path))
		}
		var n int
		n, err = j.Extract(in.Path, dest)
		result["files"] = n
	case "compress":
		if in.Dest == "" || !strings.HasSuffix(in.Dest, ".tar.gz") {
			badRequest(w, "dest must end with .tar.gz")
			return
		}
		err = j.Compress(in.Paths, in.Dest)
	default:
		badRequest(w, "unknown operation")
		return
	}
	if err != nil {
		s.fileErr(w, r, err)
		return
	}
	s.audit(r, "file."+op, d.Name, strings.TrimSpace(in.Path+" "+in.From+" "+in.To+" "+in.Dest))
	writeJSON(w, http.StatusOK, result)
}
