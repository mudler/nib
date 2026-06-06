package loop

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(durable, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Load reads durable jobs from path and registers them (re-parsing exprs and
// recomputing next-fire from the current clock). A missing file is not an
// error. Returns the number of jobs loaded. One-shot jobs whose fire time has
// already passed are dropped.
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
			continue // drop jobs that no longer parse
		}
		_ = added
		loaded++
	}
	return loaded, nil
}
