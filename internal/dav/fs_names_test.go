package dav_test

import (
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/dav"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// A shared workspace that is also called My files never takes the user's own My files path.
func TestWorkspaceNamesKeepMyFilesForThePersonalWorkspace(t *testing.T) {
	names := dav.WorkspaceNames([]drive.Workspace{{Kind: "shared", ID: "a-shared", Name: "My files"}, {Kind: "personal", ID: "z-personal", Name: "My files"}})
	if names["My files"] != "z-personal" || names["My files (2)"] != "a-shared" {
		t.Fatalf("names: %v", names)
	}
}
