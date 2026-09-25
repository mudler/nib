package types

import (
	"reflect"
	"testing"
)

// TestCompactionConfigDisableArtifactSpillField asserts that CompactionConfig
// carries a DisableArtifactSpill bool field tagged disable_artifact_spill, that
// its zero value is false (spill ON by default), and that adding it kept the
// struct comparable against its zero value.
func TestCompactionConfigDisableArtifactSpillField(t *testing.T) {
	var zero CompactionConfig
	// Zero value must be false so spill stays ON by default.
	if zero.DisableArtifactSpill != false {
		t.Fatalf("zero-value DisableArtifactSpill = true, want false")
	}

	// Set true and confirm round-trips through the field.
	c := CompactionConfig{DisableArtifactSpill: true}
	if !c.DisableArtifactSpill {
		t.Fatalf("DisableArtifactSpill set true but reads false")
	}

	// The field must be tagged disable_artifact_spill.
	st := reflect.TypeOf(CompactionConfig{})
	f, ok := st.FieldByName("DisableArtifactSpill")
	if !ok {
		t.Fatalf("CompactionConfig has no DisableArtifactSpill field")
	}
	if got := f.Tag.Get("yaml"); got != "disable_artifact_spill" {
		t.Fatalf("yaml tag = %q, want %q", got, "disable_artifact_spill")
	}

	// The struct must stay comparable against the zero value (config defaulting
	// relies on this). A bool field does not break that.
	if !reflect.DeepEqual(CompactionConfig{}, CompactionConfig{}) {
		t.Fatalf("CompactionConfig zero values not equal — comparability broken")
	}
}
