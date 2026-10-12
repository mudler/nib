// Package cataloggen implements reproducible catalog maintenance, not discovery.
package cataloggen

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/mudler/nib/llmprovider/catalog"
)

const Repository = "https://github.com/can1357/oh-my-pi"
const SourcePath = "packages/catalog/src/models.json"
const MaxBytes = 32 << 20

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Provenance struct {
	Repository        string `json:"repository"`
	Path              string `json:"path"`
	Revision          string `json:"revision"`
	SHA256            string `json:"sha256"`
	ValidationVersion int    `json:"validationVersion"`
}

func ReadProvenance(dir string) (Provenance, error) {
	var p Provenance
	b, e := os.ReadFile(filepath.Join(dir, "provenance.json"))
	if e != nil {
		return p, e
	}
	e = json.Unmarshal(b, &p)
	if e == nil && (p.Repository != Repository || p.Path != SourcePath || !commitPattern.MatchString(p.Revision) || len(p.SHA256) != 64 || p.ValidationVersion != 1) {
		e = fmt.Errorf("unsupported provenance")
	}
	return p, e
}
func fetch(client *http.Client, url string) ([]byte, error) {
	c := *client
	if c.Timeout <= 0 || c.Timeout > 30*time.Second {
		c.Timeout = 30 * time.Second
	}
	resp, e := c.Get(url)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: %s", resp.Status)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if e == nil && len(b) > MaxBytes {
		e = fmt.Errorf("source exceeds %d bytes", MaxBytes)
	}
	return b, e
}

// ResolveMain resolves the moving reference once; callers fetch only this commit.
func ResolveMain(client *http.Client) (string, error) {
	b, e := fetch(client, "https://api.github.com/repos/can1357/oh-my-pi/commits/main")
	if e != nil {
		return "", e
	}
	var v struct {
		SHA string `json:"sha"`
	}
	if e = json.Unmarshal(b, &v); e != nil {
		return "", e
	}
	if !commitPattern.MatchString(v.SHA) {
		return "", fmt.Errorf("invalid upstream revision")
	}
	return v.SHA, nil
}
func SourceURL(revision string) string {
	return "https://raw.githubusercontent.com/can1357/oh-my-pi/" + revision + "/" + SourcePath
}

type records map[string]map[string]map[string]json.RawMessage

// validateType checks precisely the consumed schema while allowing unknown fields.
func validateType(raw json.RawMessage, typ reflect.Type) error {
	if bytes.Equal(raw, []byte("null")) {
		if typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			return nil
		}
		return fmt.Errorf("unexpected null")
	}
	if typ.Kind() == reflect.Pointer {
		return validateType(raw, typ.Elem())
	}
	if typ.Kind() == reflect.Struct {
		var m map[string]json.RawMessage
		if e := json.Unmarshal(raw, &m); e != nil {
			return e
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			key := strings.Split(f.Tag.Get("json"), ",")[0]
			if v, ok := m[key]; ok {
				if e := validateType(v, f.Type); e != nil {
					return fmt.Errorf("%s: %w", key, e)
				}
			}
		}
		return nil
	}
	v := reflect.New(typ)
	if e := json.Unmarshal(raw, v.Interface()); e != nil {
		return e
	}
	if typ.Kind() == reflect.Int && (v.Elem().Int() <= 0 || v.Elem().Int() > 1<<53-1) {
		return fmt.Errorf("limit must be a positive safe integer")
	}
	return nil
}
func validate(raw []byte) (records, error) {
	var db records
	if e := json.Unmarshal(raw, &db); e != nil {
		return nil, e
	}
	if len(db) == 0 {
		return nil, fmt.Errorf("empty catalog")
	}
	for p, models := range db {
		if strings.TrimSpace(p) == "" || len(models) == 0 {
			return nil, fmt.Errorf("empty provider %q", p)
		}
		for id, m := range models {
			var mid, provider string
			json.Unmarshal(m["id"], &mid)
			json.Unmarshal(m["provider"], &provider)
			if strings.TrimSpace(id) == "" || mid != id || provider != p {
				return nil, fmt.Errorf("record keys disagree: %s/%s", p, id)
			}
			b, _ := json.Marshal(m)
			if e := validateType(b, reflect.TypeOf(catalog.Model{})); e != nil {
				return nil, fmt.Errorf("%s/%s: %w", p, id, e)
			}
			// Capabilities used by suggestion consumers are validated too.
			for key, typ := range map[string]reflect.Type{"reasoning": reflect.TypeOf(false), "input": reflect.TypeOf([]string{}), "supportsComputerUse": reflect.TypeOf(false)} {
				if v, ok := m[key]; ok {
					if e := validateType(v, typ); e != nil {
						return nil, fmt.Errorf("%s/%s %s: %w", p, id, key, e)
					}
				}
			}
		}
	}
	return db, nil
}
func projection(m map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	typ := reflect.TypeOf(catalog.Model{})
	for i := 0; i < typ.NumField(); i++ {
		key := typ.Field(i).Tag.Get("json")
		if key == "compat" {
			var c catalog.Compat
			json.Unmarshal(m[key], &c)
			out[key], _ = json.Marshal(c)
		} else {
			out[key] = m[key]
		}
	}
	for _, key := range []string{"reasoning", "input", "supportsComputerUse", "thinking"} {
		out[key] = m[key]
	}
	return out
}
func delta(old, new records) string {
	var providers, models, changes []string
	for p, ms := range new {
		if _, ok := old[p]; !ok {
			providers = append(providers, "+ provider "+p)
		}
		for id, m := range ms {
			before, ok := old[p][id]
			if !ok {
				models = append(models, "+ model "+p+"/"+id)
				continue
			}
			a, b := projection(before), projection(m)
			for k, v := range b {
				if !bytes.Equal(a[k], v) {
					changes = append(changes, fmt.Sprintf("~ %s/%s %s: %s -> %s", p, id, k, display(a[k]), display(v)))
				}
			}
		}
	}
	for p, ms := range old {
		if _, ok := new[p]; !ok {
			providers = append(providers, "- provider "+p)
		}
		for id := range ms {
			if _, ok := new[p][id]; !ok {
				models = append(models, "- model "+p+"/"+id)
			}
		}
	}
	var all []string
	for _, lines := range [][]string{providers, models, changes} {
		sort.Strings(lines)
		all = append(all, lines...)
	}
	if len(all) == 0 {
		return ""
	}
	return strings.Join(all, "\n") + "\n"
}
func display(v []byte) string {
	if len(v) == 0 {
		return "<absent>"
	}
	return string(v)
}

// Generate validates everything before preparing same-directory temporary files.
// Reproduction checks the pinned digest; refresh deliberately replaces provenance.
func Generate(client *http.Client, sourceURL, dir, revision string, refresh bool) (string, error) {
	if !commitPattern.MatchString(revision) {
		return "", fmt.Errorf("revision must be a full immutable commit SHA")
	}
	raw, e := fetch(client, sourceURL)
	if e != nil {
		return "", e
	}
	db, e := validate(raw)
	if e != nil {
		return "", e
	}
	p := Provenance{Repository, SourcePath, revision, fmt.Sprintf("%x", sha256.Sum256(raw)), 1}
	if !refresh {
		pin, e := ReadProvenance(dir)
		if e != nil {
			return "", e
		}
		if pin != p {
			return "", fmt.Errorf("source does not match pinned provenance")
		}
	}
	var old records
	previous, e := os.ReadFile(filepath.Join(dir, "catalog.json.zst"))
	if e == nil {
		dec, e := zstd.NewReader(nil)
		if e != nil {
			return "", e
		}
		b, e := dec.DecodeAll(previous, nil)
		dec.Close()
		if e != nil {
			return "", fmt.Errorf("old artifact: %w", e)
		}
		if e = json.Unmarshal(b, &old); e != nil {
			return "", e
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	summary := delta(old, db)
	enc, e := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(19)), zstd.WithEncoderConcurrency(1))
	if e != nil {
		return "", e
	}
	compressed := enc.EncodeAll(raw, nil)
	enc.Close()
	meta, _ := json.MarshalIndent(p, "", "  ")
	meta = append(meta, '\n')
	// Stage both before replacing either; malformed input never touches outputs.
	paths := []string{"catalog.json.zst", "provenance.json"}
	payload := [][]byte{compressed, meta}
	// Keep a staged copy for rollback if the second rename fails. This is not
	// a cross-file crash transaction: an interrupted process may require rerunning
	// generation with the old provenance to restore the pair.
	if previous != nil {
		paths = append(paths, "catalog.rollback")
		payload = append(payload, previous)
	}
	temps := make([]string, len(paths))
	defer func() {
		for _, n := range temps {
			if n != "" {
				os.Remove(n)
			}
		}
	}()
	for i, n := range paths {
		f, e := os.CreateTemp(dir, "."+n+"-*")
		if e != nil {
			return "", e
		}
		temps[i] = f.Name()
		e = f.Chmod(0644)
		if e == nil {
			_, e = f.Write(payload[i])
		}
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return "", e
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	for i, n := range paths[:2] {
		if e := os.Rename(temps[i], filepath.Join(dir, n)); e != nil {
			if i == 1 {
				var rollbackErr error
				artifact := filepath.Join(dir, paths[0])
				if previous != nil {
					rollbackErr = os.Rename(temps[2], artifact)
				} else {
					rollbackErr = os.Remove(artifact)
				}
				if rollbackErr != nil {
					// Preserve the backup and name it explicitly for manual recovery.
					backup := "<no previous artifact>"
					if previous != nil {
						backup, temps[2] = temps[2], ""
					}
					return "", fmt.Errorf("replace %s: %v; rollback failed: %v; backup: %s; rerun pinned generation to recover", n, e, rollbackErr, backup)
				}
			}
			return "", fmt.Errorf("replace %s: %w", n, e)
		}
	}
	return summary, nil
}
