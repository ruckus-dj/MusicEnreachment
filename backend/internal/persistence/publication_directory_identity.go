package persistence

// PublicationDirectoryIdentity is a stable identity read from an already-open
// directory handle. It is used by publication cleanup authority.
type PublicationDirectoryIdentity struct {
	Platform string `json:"platform"`
	Volume   string `json:"volume"`
	FileID   string `json:"file_id"`
}
