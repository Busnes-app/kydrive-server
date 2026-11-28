package vaultclient

import "github.com/Busnes-app/kydrive-server/internal/vault"

type (
	Binding    = vault.Binding
	Checkpoint = vault.Checkpoint
	Head       = vault.Head
	Receipt    = vault.Receipt
	Record     = vault.Record
	// RecoveryRecord is one signed link of an account's recovery chain.
	RecoveryRecord = vault.RecoveryRecord
	Revision       = vault.Revision
	State          = vault.State
)

const MaxDocumentBytes = vault.MaxDocumentBytes

func ValidID(id string) bool     { return vault.ID(id) }
func ValidUserID(id string) bool { return vault.ValidUserID(id) }
