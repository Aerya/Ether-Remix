package events

// Canonical vault event type names.
const (
	EventVaultRecordStored   = "vault.record.stored"
	EventVaultRecordDead     = "vault.record.dead"
	EventVaultPayloadMissing = "vault.payload.missing"
	EventVaultPackCompacted  = "vault.pack.compacted"
	EventVaultQuotaWarning   = "vault.quota.warning"
)

// VaultRecordStoredPayload is emitted when a release record is stored in the vault.
type VaultRecordStoredPayload struct {
	VaultKey        string `json:"vaultKey" event:"label=Vault key;example=nzb:sha256:abc123"`
	Kind            string `json:"kind" event:"label=Record kind;example=nzb"`
	TitleKey        string `json:"titleKey,omitempty" event:"label=Title key;example=movie:603"`
	ReleaseName     string `json:"releaseName,omitempty" event:"label=Release name;example=The.Matrix.1999.2160p.REMUX"`
	SizeBytes       int64  `json:"sizeBytes" event:"label=Uncompressed size;unit=bytes;example=163840"`
	CompressedBytes int64  `json:"compressedBytes" event:"label=Compressed size;unit=bytes;example=48210"`
	PackID          string `json:"packId,omitempty" event:"label=Pack ID;example=pack_0001"`
}

// VaultRecordDeadPayload is emitted when a vault record is marked dead.
type VaultRecordDeadPayload struct {
	VaultKey    string `json:"vaultKey" event:"label=Vault key;example=nzb:sha256:abc123"`
	Kind        string `json:"kind,omitempty" event:"label=Record kind;example=nzb"`
	TitleKey    string `json:"titleKey,omitempty" event:"label=Title key;example=movie:603"`
	ReleaseName string `json:"releaseName,omitempty" event:"label=Release name;example=The.Matrix.1999.2160p.REMUX"`
	Reason      string `json:"reason,omitempty" event:"label=Reason;example=missing articles"`
}

// VaultPayloadMissingPayload is emitted when a vault record's pack payload cannot be read.
type VaultPayloadMissingPayload struct {
	VaultKey string `json:"vaultKey" event:"label=Vault key;example=nzb:sha256:abc123"`
	PackID   string `json:"packId,omitempty" event:"label=Pack ID;example=pack_0001"`
	NodeID   string `json:"nodeId,omitempty" event:"label=Node ID;example=node-1"`
	TitleKey string `json:"titleKey,omitempty" event:"label=Title key;example=movie:603"`
}

// VaultPackCompactedPayload is emitted when vault pack compaction finishes.
type VaultPackCompactedPayload struct {
	PacksCompacted int   `json:"packsCompacted" event:"label=Packs compacted;example=3"`
	BytesReclaimed int64 `json:"bytesReclaimed" event:"label=Bytes reclaimed;unit=bytes;example=52428800"`
	DurationMS     int64 `json:"durationMs" event:"label=Compaction duration;duration_ms;unit=ms;example=840"`
}

// VaultQuotaWarningPayload is emitted when vault disk usage nears its configured cap.
type VaultQuotaWarningPayload struct {
	UsedBytes   int64   `json:"usedBytes" event:"label=Used bytes;unit=bytes;example=858993459"`
	BudgetBytes int64   `json:"budgetBytes" event:"label=Budget bytes;unit=bytes;example=1073741824"`
	PercentUsed float64 `json:"percentUsed" event:"label=Percent used;unit=%;example=80"`
	RecordCount int     `json:"recordCount" event:"label=Record count;example=1420"`
}

// RegisterVaultSchemas registers all built-in vault event schemas.
func RegisterVaultSchemas(r *Registry) {
	registerMediaSchema[VaultRecordStoredPayload](r, EventSchema{
		Type:        EventVaultRecordStored,
		Version:     1,
		Domain:      "vault",
		Label:       "Vault record stored",
		Description: "Emitted when a release payload is stored in the vault.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[VaultRecordDeadPayload](r, EventSchema{
		Type:        EventVaultRecordDead,
		Version:     1,
		Domain:      "vault",
		Label:       "Vault record marked dead",
		Description: "Emitted when a stored vault release is marked dead.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[VaultPayloadMissingPayload](r, EventSchema{
		Type:        EventVaultPayloadMissing,
		Version:     1,
		Domain:      "vault",
		Label:       "Vault payload missing",
		Description: "Emitted when a vault index entry references a missing pack payload.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[VaultPackCompactedPayload](r, EventSchema{
		Type:        EventVaultPackCompacted,
		Version:     1,
		Domain:      "vault",
		Label:       "Vault packs compacted",
		Description: "Emitted when vault pack compaction reclaims space.",
		Owner:       OwnerBuiltin,
	})
	registerMediaSchema[VaultQuotaWarningPayload](r, EventSchema{
		Type:        EventVaultQuotaWarning,
		Version:     1,
		Domain:      "vault",
		Label:       "Vault quota warning",
		Description: "Emitted when vault storage usage crosses the warning threshold.",
		Owner:       OwnerBuiltin,
	})
}

func init() {
	registerBuiltin(RegisterVaultSchemas)
}
