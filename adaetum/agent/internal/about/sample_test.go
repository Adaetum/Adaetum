package about

import (
	"os"
	"path/filepath"
	"testing"
)

// The checked-in descriptions must parse with the agent's own reader.
func TestRepositoryDescriptionsParse(t *testing.T) {
	files, _ := filepath.Glob("../../../../pods/*/*" + FileSuffix)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(data); err != nil {
			t.Errorf("%s: %v", file, err)
		}
	}
}
