package buildstate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/karl-tye/ktml/internal/compiler"
)

func TestHashWorkspace_GIVENImportedHostSchema_WHENSchemaChanges_THENDependentHashChanges(t *testing.T) {
	dir := t.TempDir()
	schema := filepath.Join(dir, "user.ktmlschema")
	card := filepath.Join(dir, "card.ktml")
	if err := os.WriteFile(card, []byte("import { User } from \"./user.ktmlschema\"\nCard [user: User] { \"{user.name}\" }"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash := func(schemaSource string) string {
		t.Helper()
		if err := os.WriteFile(schema, []byte(schemaSource), 0o600); err != nil {
			t.Fatal(err)
		}
		snapshot, err := compiler.NewWorkspace(dir).Load(card)
		if err != nil || !snapshot.Valid() {
			t.Fatalf("Load() = %v, %v", err, snapshot.Err())
		}
		return HashWorkspace(snapshot, card)
	}
	before := hash("record User [name: string]")
	after := hash("record User [name: string, id: string]")
	if before == after {
		t.Fatalf("dependent hash did not change after a schema edit")
	}
	if again := hash("record User [name: string, id: string]"); again != after {
		t.Fatalf("dependent hash is not deterministic")
	}
}
