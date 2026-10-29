package store

type CacheMode string

const (
	CacheModeOff     CacheMode = "off"
	CacheModeMinimal CacheMode = "minimal"
	CacheModeWrites  CacheMode = "writes"
	CacheModeFull    CacheMode = "full"
)

const (
	ConfigKeyUIType  = "_uiType"
	ConfigKeyPassEnv = "_passenv"
	ConfigKeyOAuth   = "_oauth"
	ConfigKeyThumbs  = "_thumbs" // phone drive: "false" turns off thumbnail pre-loading
)

// Connection stores everything needed to create and mount an rclone remote.
//
// Provider is the rclone remote type ("ftp", "s3", "drive", ).
// Config holds provider-specific key=value pairs passed to rclone config create.
// Internal metadata keys start with "_" and are not forwarded to rclone:
//
//	_uiType    the UI provider key (e.g. "ftps", "s3-r2") for re-selecting the form
//	_passenv   suffix of the RCLONE_CONFIG_*_<suffix> env var for the password
//	_oauth     "true" if this is an OAuth provider
type Connection struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Provider      string            `json:"provider"`
	Config        map[string]string `json:"config"`
	DriveLetter   string            `json:"drive_letter"`
	RemotePath    string            `json:"remote_path"`
	CacheMode     CacheMode         `json:"cache_mode"`
	AutoMount     bool              `json:"auto_mount"`
	AutoReconnect bool              `json:"auto_reconnect"`

	// VFS / mount tuning (passed as rclone mount flags, not rclone remote config).
	// Empty string means "use rclone's default for that flag".
	CacheMaxSize  string `json:"cache_max_size,omitempty"`  // --vfs-cache-max-size  e.g. "1G"
	CacheMaxAge   string `json:"cache_max_age,omitempty"`   // --vfs-cache-max-age   e.g. "1h"
	DirCacheTime  string `json:"dir_cache_time,omitempty"`  // --dir-cache-time      e.g. "5m"
	BufferSize    string `json:"buffer_size,omitempty"`     // --buffer-size         e.g. "16M"
	ReadChunkSize string `json:"read_chunk_size,omitempty"` // --vfs-read-chunk-size e.g. "128M"

	// v1 legacy fields  present only in old JSON files; cleared after migration.
	LegacyProtocol string `json:"protocol,omitempty"`
	LegacyHost     string `json:"host,omitempty"`
	LegacyPort     int    `json:"port,omitempty"`
	LegacyUsername string `json:"username,omitempty"`
	LegacySSHKey   string `json:"ssh_key_path,omitempty"`
}

// RcloneType returns the rclone remote type string (may differ from Provider, e.g. "ftps""ftp").
func (c *Connection) RcloneType() string { return RcloneTypeFor(c.Provider) }

// RcloneTypeFor maps our UI provider key to the rclone backend type.
func RcloneTypeFor(provider string) string {
	switch provider {
	case "ftps":
		return "ftp"
	}
	return provider
}

// DefaultPort returns a sensible default TCP port for simple server-based providers.
func DefaultPort(provider string) int {
	switch provider {
	case "sftp":
		return 22
	case "smb":
		return 445
	case "webdav":
		return 443
	default:
		return 21
	}
}
