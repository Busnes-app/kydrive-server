package editor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/config"
)

func TestEditorReplicaFingerprint(t *testing.T) {
	for name, value := range map[string]string{"KYDRIVE_EDITOR_URL": "https://office.example.test/", "KYDRIVE_EDITOR_DRIVE_URL": "", "KYDRIVE_EDITOR_INTERNAL_URL": "", "KYDRIVE_EDITOR_SECRET": "private-editor-marker-32-bytes-long"} {
		t.Setenv(name, value)
	}
	key := bytes.Repeat([]byte{7}, 32)
	ed, err := FromEnv("https://drive.example.test", "production")
	if err != nil {
		t.Fatal(err)
	}
	original := ed.ReplicaFingerprint(key)
	raw, _ := json.Marshal(original)
	if bytes.Contains(raw, []byte("private-editor")) {
		t.Fatal("editor secret exposed")
	}
	t.Setenv("KYDRIVE_EDITOR_URL", "https://office.example.test")
	t.Setenv("KYDRIVE_EDITOR_DRIVE_URL", "https://drive.example.test/")
	t.Setenv("KYDRIVE_EDITOR_INTERNAL_URL", "https://office.example.test/")
	other, err := FromEnv("https://drive.example.test", "production")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, other.ReplicaFingerprint(key)) {
		t.Fatal("effective defaults did not canonicalize")
	}
	other.secret = []byte("another-32-byte-editor-secret-marker")
	// Include the deployment check for the same comparison contract as the whole product.
	original[config.EncryptionKeyField] = config.ReplicaField(key, config.EncryptionKeyField, "")
	changed := other.ReplicaFingerprint(key)
	changed[config.EncryptionKeyField] = original[config.EncryptionKeyField]
	if got := config.DiffFingerprint(original, changed); !slices.Equal(got, []string{"KYDRIVE_EDITOR_SECRET"}) {
		t.Fatal(got)
	}
	var disabled *Client
	if reflect.DeepEqual(disabled.ReplicaFingerprint(key), ed.ReplicaFingerprint(key)) {
		t.Fatal("disabling editor went undetected")
	}
}
