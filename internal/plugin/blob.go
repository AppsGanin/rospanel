package plugin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// panel.blob: data too big for the plugin's heap — a backup to upload, a file to
// download — kept by the host in a temporary file and handed to the plugin as a
// handle {blob, size}. A blob lives for one call: the files are removed when it
// returns, so nothing lingers on the box after a plugin that forgot them.

const (
	maxBlob      = 256 << 20 // one blob
	maxBlobTotal = 512 << 20 // all of one call's blobs
	maxBlobs     = 16
	maxBlobText  = 4 << 20 // blob.text into the heap
)

// blobSet is one call's blobs (instance.mu held: one call at a time).
type blobSet struct {
	dir   string
	files map[string]*blobFile
	total int64
}

type blobFile struct {
	path string
	size int64
}

// blobHandle is what JS gets.
type blobHandle struct {
	Blob string `json:"blob"`
	Size int64  `json:"size"`
}

func (inst *instance) blobDir() string {
	return filepath.Join(inst.host.deps.DataDir, "plugins", ".blobs")
}

// newBlob creates an empty blob and returns it with a writer that refuses more
// than the limits allow.
func (inst *instance) newBlob() (string, *blobWriter, error) {
	if inst.blobs == nil {
		dir := inst.blobDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", nil, err
		}
		tmp, err := os.MkdirTemp(dir, inst.id+"-")
		if err != nil {
			return "", nil, err
		}
		inst.blobs = &blobSet{dir: tmp, files: map[string]*blobFile{}}
	}
	b := inst.blobs
	if len(b.files) >= maxBlobs {
		return "", nil, fmt.Errorf("blob: at most %d per call", maxBlobs)
	}
	var id [6]byte
	_, _ = rand.Read(id[:])
	name := "b" + hex.EncodeToString(id[:])
	path := filepath.Join(b.dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, err
	}
	bf := &blobFile{path: path}
	b.files[name] = bf
	return name, &blobWriter{f: f, file: bf, set: b}, nil
}

// blobWriter counts what is written against the blob and per-call limits.
type blobWriter struct {
	f    *os.File
	file *blobFile
	set  *blobSet
}

var errBlobTooBig = errors.New("blob: over the size limit (256 MB each, 512 MB per call)")

func (w *blobWriter) Write(p []byte) (int, error) {
	if w.file.size+int64(len(p)) > maxBlob || w.set.total+int64(len(p)) > maxBlobTotal {
		return 0, errBlobTooBig
	}
	n, err := w.f.Write(p)
	w.file.size += int64(n)
	w.set.total += int64(n)
	return n, err
}

func (w *blobWriter) Close() error { return w.f.Close() }

// openBlob opens a blob of this call for reading.
func (inst *instance) openBlob(name string) (*os.File, int64, error) {
	if inst.blobs == nil || inst.blobs.files[name] == nil {
		return nil, 0, fmt.Errorf("blob %q: not a blob of this call", name)
	}
	bf := inst.blobs.files[name]
	f, err := os.Open(bf.path)
	return f, bf.size, err
}

// dropBlobs removes this call's blobs.
func (inst *instance) dropBlobs() {
	if inst.blobs != nil {
		_ = os.RemoveAll(inst.blobs.dir)
		inst.blobs = nil
	}
}

// opBlob answers panel.blob.*.
func (inst *instance) opBlob(op string, arg []byte) (any, error) {
	switch op {
	case "blob.from":
		a, err := decode[struct {
			Data dataValue `json:"data"`
		}](arg)
		if err != nil {
			return nil, err
		}
		name, w, err := inst.newBlob()
		if err != nil {
			return nil, err
		}
		_, err = w.Write(a.Data)
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		return blobHandle{Blob: name, Size: int64(len(a.Data))}, nil
	case "blob.hash":
		a, err := decode[struct {
			Blob string `json:"blob"`
			Alg  string `json:"alg"`
			Enc  string `json:"enc"`
		}](arg)
		if err != nil {
			return nil, err
		}
		h, err := hasher(a.Alg)
		if err != nil {
			return nil, err
		}
		f, _, err := inst.openBlob(a.Blob)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if _, err := io.Copy(h, f); err != nil {
			return nil, err
		}
		return encode(h.Sum(nil), a.Enc)
	case "blob.text":
		a, err := decode[struct {
			Blob string `json:"blob"`
			Enc  string `json:"enc"` // "" = UTF-8 text, or base64
		}](arg)
		if err != nil {
			return nil, err
		}
		f, size, err := inst.openBlob(a.Blob)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if size > maxBlobText {
			return nil, fmt.Errorf("blob.text: %d MB is too big for the plugin's memory (%d MB)", size>>20, maxBlobText>>20)
		}
		b, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		if a.Enc == "base64" {
			return base64.StdEncoding.EncodeToString(b), nil
		}
		if !utf8.Valid(b) {
			return nil, errors.New(`blob.text: not UTF-8 text — use {enc: "base64"}`)
		}
		return string(b), nil
	}
	return nil, fmt.Errorf("unknown operation %q", op)
}

// FormPart is one field of a multipart/form-data body: a value, or a blob sent as a file.
type FormPart struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Blob     string `json:"blob,omitempty"`
	Filename string `json:"filename,omitempty"`
	Type     string `json:"type,omitempty"`
}

// fetchBody turns a request's blob body or form into a stream for the fetcher.
func (inst *instance) fetchBody(ctx context.Context, req *FetchRequest) (func(), error) {
	switch {
	case req.BodyBlob != "" && len(req.Form) > 0:
		return nil, errors.New("fetch: body or form, not both")
	case req.BodyBlob != "":
		f, size, err := inst.openBlob(req.BodyBlob)
		if err != nil {
			return nil, err
		}
		req.Body, req.BodyReader, req.BodySize = "", f, size
		return func() { f.Close() }, nil
	case len(req.Form) > 0:
		var files []*os.File
		closeAll := func() {
			for _, f := range files {
				f.Close()
			}
		}
		for _, p := range req.Form {
			if p.Blob == "" {
				continue
			}
			f, _, err := inst.openBlob(p.Blob)
			if err != nil {
				closeAll()
				return nil, err
			}
			files = append(files, f)
		}
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		parts := req.Form
		go func() {
			i := 0
			var err error
			for _, p := range parts {
				if ctx.Err() != nil {
					err = ctx.Err()
					break
				}
				if p.Blob == "" {
					if err = mw.WriteField(p.Name, p.Value); err != nil {
						break
					}
					continue
				}
				h := textproto.MIMEHeader{}
				fn := p.Filename
				if fn == "" {
					fn = p.Name
				}
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, quoteForm(p.Name), quoteForm(fn)))
				ct := p.Type
				if ct == "" {
					ct = "application/octet-stream"
				}
				h.Set("Content-Type", ct)
				var w io.Writer
				if w, err = mw.CreatePart(h); err != nil {
					break
				}
				if _, err = io.Copy(w, files[i]); err != nil {
					break
				}
				i++
			}
			if err == nil {
				err = mw.Close()
			}
			pw.CloseWithError(err)
		}()
		req.Body, req.BodyReader, req.BodySize = "", pr, -1
		if req.Headers == nil {
			req.Headers = map[string]string{}
		}
		req.Headers["Content-Type"] = mw.FormDataContentType()
		return func() { pr.Close(); closeAll() }, nil
	}
	return func() {}, nil
}

func quoteForm(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "").Replace(s)
}
