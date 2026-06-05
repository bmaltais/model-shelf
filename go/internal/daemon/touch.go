package daemon

import "log"

// TouchModel updates the last-accessed timestamp for a model in the persisted
// inventory. This is called from the CLI resolve path (local, no daemon needed).
func TouchModel(repoID, format, quant string, sizeBytes int64) {
	inv, err := LoadInventory()
	if err != nil {
		log.Printf("inventory: failed to load for touch: %v", err)
		return
	}
	inv.Touch(repoID, format, quant, sizeBytes)
	if err := inv.Save(); err != nil {
		log.Printf("inventory: failed to save after touch: %v", err)
	}
}
