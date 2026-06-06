package loop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Save writes the durable jobs to path as JSON, creating parent dirs. Jobs with
// Durable == false are skipped. Re-parses are deferred to Load.
func (r *Registry) Save(path string) error {
	r.mu.Lock()
	var durable []Job
	for _, j := range r.jobs {
		if j.Durable {
			durable = append(durable, j)
		}
	}
	r.mu.Unlock()

	if len(durable) == 0 {
		// Nothing durable: remove any stale file so reloads stay clean.
		_ = os.Remove(path)
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(durable, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".loops-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// setCreated overwrites the Created timestamp of the job with the given id.
// Used by Load to preserve original creation times across a restart.
func (r *Registry) setCreated(id string, created time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.jobs {
		if r.jobs[i].ID == id {
			r.jobs[i].Created = created
			return
		}
	}
}

// Load reads durable jobs from path and registers them (re-parsing exprs and
// recomputing next-fire from the current clock). A missing file is not an
// error. Returns the number of jobs loaded. Jobs are re-registered with
// next-fire recomputed from the current clock; jobs whose expression no longer
// parses (or can never fire) are skipped. Job IDs are reassigned on load (use
// List to see current IDs); the original Created timestamp is preserved.
func (r *Registry) Load(path string) (int, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var stored []Job
	if err := json.Unmarshal(data, &stored); err != nil {
		return 0, err
	}
	loaded := 0
	for _, j := range stored {
		added, err := r.Add(j.Expr, j.Prompt, j.Recurring, true)
		if err != nil {
			continue // drop jobs that no longer parse or can never fire
		}
		if !j.Created.IsZero() {
			r.setCreated(added.ID, j.Created)
		}
		loaded++
	}
	return loaded, nil
}
