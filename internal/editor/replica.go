package editor

import "github.com/Busnes-app/kydrive-server/internal/config"

// ReplicaFingerprint adds the validated effective editor configuration without exporting
// its signing secret. A disabled editor has explicit empty fields, so enable/disable differs.
func (c *Client) ReplicaFingerprint(key []byte) map[string]string {
	fields := map[string]string{"KYDRIVE_EDITOR_URL": "", "KYDRIVE_EDITOR_DRIVE_URL": "", "KYDRIVE_EDITOR_INTERNAL_URL": "", "KYDRIVE_EDITOR_SECRET": ""}
	if c != nil {
		fields["KYDRIVE_EDITOR_URL"] = c.Origin
		fields["KYDRIVE_EDITOR_DRIVE_URL"] = c.DriveOrigin
		fields["KYDRIVE_EDITOR_INTERNAL_URL"] = c.InternalOrigin
		fields["KYDRIVE_EDITOR_SECRET"] = string(c.secret)
	}
	for name, value := range fields {
		fields[name] = config.ReplicaField(key, name, value)
	}
	return fields
}
