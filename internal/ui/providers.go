package ui

import (
	"strings"

	"wintp/internal/store"
)

// FormField describes one input in the provider-specific form.
type FormField struct {
	Key         string
	Label       string
	Placeholder string
	Default     string
	IsPassword  bool
	Optional    bool
	// Row groups short fields (e.g. host + port) onto one horizontal line
	// instead of each taking a full-width row. Fields sharing the same
	// non-empty Row value, and appearing consecutively in Fields, are laid
	// out side by side. Leave empty for a normal full-width field.
	Row string
}

// ProviderDef describes how to build the UI for one rclone provider.
type ProviderDef struct {
	Type          string            // stored in Connection.Config[store.ConfigKeyUIType] and used for lookup
	RcloneType    string            // actual rclone remote type (may differ, e.g. "ftps""ftp")
	Name          string            // display name in the provider dropdown
	Subtitle      string            // short hint shown under Name in the provider picker card ("" if not a featured provider)
	Group         string            // visual grouping label
	Fields        []FormField       // form fields shown in the editor
	PresetConfig  map[string]string // extra Config keys written automatically (hidden from UI)
	IsOAuth       bool              // uses browser OAuth; no password field
	PassEnvSuffix string            // RCLONE_CONFIG_*_<suffix> env-var for password; "" for OAuth
	PasswordField string            // Field.Key whose value goes to Windows Credential Manager
}

// featuredProviderTypes lists the Type values shown as quick-pick cards at
// the top of the provider picker, in display order. Every other provider
// remains reachable through the "More providers" dropdown.
var featuredProviderTypes = []string{"ftp", "sftp", "webdav", "s3-aws", "gdrive", "onedrive"}

// featuredSubtitles gives each featured provider its one-line hint.
var featuredSubtitles = map[string]string{
	"ftp":      "Classic file server",
	"sftp":     "Over SSH",
	"webdav":   "Nextcloud, NAS",
	"s3-aws":   "AWS, R2, MinIO",
	"gdrive":   "Sign in with Google",
	"onedrive": "Sign in with Microsoft",
}

// FeaturedProviders returns the curated provider list for the picker's card
// grid, in display order.
func FeaturedProviders() []*ProviderDef {
	out := make([]*ProviderDef, 0, len(featuredProviderTypes))
	for _, t := range featuredProviderTypes {
		if def := FindProvider(t); def != nil {
			out = append(out, def)
		}
	}
	return out
}

// badgeShortNames maps a provider Type to the short code shown in the
// connection list's avatar badge. Providers not listed here fall back to an
// algorithmic abbreviation (see badgeFor).
var badgeShortNames = map[string]string{
	"ftp": "FTP", "ftps": "FTPS", "sftp": "SFTP",
	"webdav": "WD", "nextcloud": "NC", "owncloud": "OC",
	"smb": "SMB", "rsyncnet": "RSY", "http": "HTTP",
	"gdrive": "GD", "gcs": "GCS", "onedrive": "OD",
	"azureblob": "AZB", "azurefiles": "AZF", "dropbox": "DBX",
	"mega": "MEGA", "seafile": "SF", "b2": "B2",
	"koofr": "KOO", "storj": "STJ", "internetarchive": "IA",
	"swift": "SWF", "hdfs": "HDFS", "sia": "SIA", "local": "LOC",
}

// badgeFor returns the short (2-4 char) code shown in a connection's avatar
// badge in the sidebar list.
func badgeFor(def *ProviderDef) string {
	if def == nil {
		return "?"
	}
	if strings.HasPrefix(def.Type, "s3") {
		return "S3"
	}
	if code, ok := badgeShortNames[def.Type]; ok {
		return code
	}
	// Fallback: initials of capitalised words in the display name.
	var letters []rune
	for _, w := range strings.Fields(def.Name) {
		r := []rune(w)
		if len(r) == 0 {
			continue
		}
		if r[0] >= 'A' && r[0] <= 'Z' {
			letters = append(letters, r[0])
			if len(letters) == 4 {
				break
			}
		}
	}
	if len(letters) > 0 {
		return string(letters)
	}
	t := strings.ToUpper(def.Type)
	if len(t) > 4 {
		t = t[:4]
	}
	return t
}

// ProviderGroups lists the display order of groups.
var ProviderGroups = []string{
	"File Transfer",
	"S3 Compatible",
	"Google",
	"Microsoft",
	"Other Cloud",
	"More",
}

// Providers is the full catalog.  All entries with the same Group are shown together.
var Providers = buildProviders()

func init() {
	for i := range Providers {
		if s, ok := featuredSubtitles[Providers[i].Type]; ok {
			Providers[i].Subtitle = s
		}
	}
}

func buildProviders() []ProviderDef {
	//  shared field sets

	serverFields := func(defaultPort string) []FormField {
		return []FormField{
			{Key: "host", Label: "Host", Placeholder: "hostname or IP", Row: "loc"},
			{Key: "port", Label: "Port", Placeholder: defaultPort, Default: defaultPort, Row: "loc"},
			{Key: "user", Label: "Username", Placeholder: "username", Row: "auth"},
			{Key: "pass", Label: "Password", IsPassword: true, Optional: true, Row: "auth"},
		}
	}

	s3Fields := func(regionHint, endpointHint string) []FormField {
		endpointOptional := endpointHint != ""
		return []FormField{
			{Key: "access_key_id", Label: "Access Key ID", Placeholder: "AKIA"},
			{Key: "secret_access_key", Label: "Secret Access Key", IsPassword: true},
			{Key: "region", Label: "Region", Placeholder: regionHint, Optional: true, Row: "loc"},
			{Key: "endpoint", Label: "Endpoint URL", Placeholder: endpointHint, Optional: endpointOptional, Row: "loc"},
		}
	}

	s3 := func(uiType, name string, preset map[string]string, regionHint, endpointHint string) ProviderDef {
		p := make(map[string]string)
		p["env_auth"] = "false"
		for k, v := range preset {
			p[k] = v
		}
		p[store.ConfigKeyPassEnv] = "SECRET_ACCESS_KEY"
		return ProviderDef{
			Type:          uiType,
			RcloneType:    "s3",
			Name:          name,
			Group:         "S3 Compatible",
			Fields:        s3Fields(regionHint, endpointHint),
			PresetConfig:  p,
			PassEnvSuffix: "SECRET_ACCESS_KEY",
			PasswordField: "secret_access_key",
		}
	}

	oauth := func(rcloneType, uiType, name, group string) ProviderDef {
		return ProviderDef{
			Type:         uiType,
			RcloneType:   rcloneType,
			Name:         name,
			Group:        group,
			IsOAuth:      true,
			PresetConfig: map[string]string{store.ConfigKeyOAuth: "true"},
		}
	}

	//  provider list

	return []ProviderDef{

		//  File Transfer

		{
			Type: "ftp", RcloneType: "ftp", Name: "FTP", Group: "File Transfer",
			Fields:        serverFields("21"),
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},
		{
			Type: "ftps", RcloneType: "ftp", Name: "FTPS (FTP over TLS)", Group: "File Transfer",
			Fields:        serverFields("21"),
			PresetConfig:  map[string]string{"tls": "true", store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},
		{
			Type: "sftp", RcloneType: "sftp", Name: "SFTP", Group: "File Transfer",
			Fields: append(serverFields("22"), FormField{
				Key: "key_file", Label: "SSH Key File",
				Placeholder: "C:\\Users\\you\\.ssh\\id_rsa (optional)", Optional: true,
			}),
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},
		{
			Type: "webdav", RcloneType: "webdav", Name: "WebDAV", Group: "File Transfer",
			Fields: []FormField{
				{Key: "url", Label: "URL", Placeholder: "https://dav.example.com"},
				{Key: "vendor", Label: "Vendor", Placeholder: "other", Default: "other"},
				{Key: "user", Label: "Username", Placeholder: "username", Row: "auth"},
				{Key: "pass", Label: "Password", IsPassword: true, Optional: true, Row: "auth"},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},
		{
			Type: "nextcloud", RcloneType: "webdav", Name: "Nextcloud", Group: "File Transfer",
			Fields: []FormField{
				{Key: "url", Label: "Server URL", Placeholder: "https://cloud.example.com/remote.php/dav/files/USER"},
				{Key: "user", Label: "Username", Placeholder: "username", Row: "auth"},
				{Key: "pass", Label: "Password", IsPassword: true, Optional: true, Row: "auth"},
			},
			PresetConfig:  map[string]string{"vendor": "nextcloud", store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},
		{
			Type: "owncloud", RcloneType: "webdav", Name: "ownCloud", Group: "File Transfer",
			Fields: []FormField{
				{Key: "url", Label: "Server URL", Placeholder: "https://cloud.example.com/remote.php/webdav"},
				{Key: "user", Label: "Username", Placeholder: "username", Row: "auth"},
				{Key: "pass", Label: "Password", IsPassword: true, Optional: true, Row: "auth"},
			},
			PresetConfig:  map[string]string{"vendor": "owncloud", store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},
		{
			Type: "smb", RcloneType: "smb", Name: "SMB / CIFS (Windows Share)", Group: "File Transfer",
			Fields: []FormField{
				{Key: "host", Label: "Host", Placeholder: "server or IP", Row: "loc"},
				{Key: "port", Label: "Port", Placeholder: "445", Default: "445", Row: "loc"},
				{Key: "user", Label: "Username", Placeholder: "username", Row: "auth"},
				{Key: "pass", Label: "Password", IsPassword: true, Optional: true, Row: "auth"},
				{Key: "domain", Label: "Domain", Placeholder: "WORKGROUP", Optional: true},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},
		{
			Type: "rsyncnet", RcloneType: "sftp", Name: "rsync.net", Group: "File Transfer",
			Fields: []FormField{
				{Key: "host", Label: "Host", Placeholder: "usw-s01.rsync.net", Row: "loc"},
				{Key: "port", Label: "Port", Placeholder: "22", Default: "22", Row: "loc"},
				{Key: "user", Label: "Username (account ID)", Placeholder: "12345", Row: "auth"},
				{Key: "pass", Label: "Password", IsPassword: true, Optional: true, Row: "auth"},
				{Key: "key_file", Label: "SSH Key File", Optional: true},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},
		{
			Type: "http", RcloneType: "http", Name: "HTTP (read-only)", Group: "File Transfer",
			Fields: []FormField{
				{Key: "url", Label: "URL", Placeholder: "https://example.com/files"},
			},
		},

		//  S3 Compatible

		s3("s3-aws", "Amazon S3", map[string]string{"provider": "AWS"}, "us-east-1", ""),
		s3("s3-r2", "Cloudflare R2", map[string]string{"provider": "Cloudflare", "no_check_bucket": "true"},
			"auto", "<accountid>.r2.cloudflarestorage.com"),
		s3("s3-b2", "Backblaze B2 (S3 Compatible)", map[string]string{"provider": "Backblaze"},
			"us-west-004", "s3.us-west-004.backblazeb2.com"),
		s3("s3-wasabi", "Wasabi", map[string]string{"provider": "Wasabi"},
			"us-east-1", "s3.wasabisys.com"),
		s3("s3-do", "DigitalOcean Spaces", map[string]string{"provider": "DigitalOcean"},
			"nyc3", "nyc3.digitaloceanspaces.com"),
		s3("s3-linode", "Linode Object Storage", map[string]string{"provider": "Linode"},
			"us-east-1", "us-east-1.linodeobjects.com"),
		s3("s3-scaleway", "Scaleway Object Storage", map[string]string{"provider": "Scaleway"},
			"nl-ams", "s3.nl-ams.scw.cloud"),
		s3("s3-idrive", "IDrive e2", map[string]string{"provider": "IDrive"},
			"", "<your-datacenter>.idrivee2-<region>.com"),
		s3("s3-ionos", "IONOS Cloud Object Storage", map[string]string{"provider": "IONOS"},
			"de", "s3-eu-central-1.ionoscloud.com"),
		s3("s3-hetzner", "Hetzner Object Storage", map[string]string{"provider": "Other"},
			"fsn1", "fsn1.your-objectstorage.com"),
		s3("s3-alibaba", "Alibaba Cloud OSS", map[string]string{"provider": "Alibaba"},
			"oss-cn-hangzhou", "oss-cn-hangzhou.aliyuncs.com"),
		s3("s3-tencent", "Tencent Cloud COS", map[string]string{"provider": "TencentCOS"},
			"ap-guangzhou", "cos.ap-guangzhou.myqcloud.com"),
		s3("s3-huawei", "Huawei OBS", map[string]string{"provider": "HuaweiOBS"},
			"cn-north-4", "obs.cn-north-4.myhuaweicloud.com"),
		s3("s3-ibm", "IBM COS S3", map[string]string{"provider": "IBMCOS"},
			"us-south-standard", "s3.us-south.cloud-object-storage.appdomain.cloud"),
		s3("s3-oracle", "Oracle Object Storage", map[string]string{"provider": "Other"},
			"us-phoenix-1", "<namespace>.compat.objectstorage.us-phoenix-1.oraclecloud.com"),
		s3("s3-storj", "Storj (S3 Compatible)", map[string]string{"provider": "Storj"},
			"", "gateway.storjshare.io"),
		s3("s3-synology", "Synology C2 Object Storage", map[string]string{"provider": "Synology"},
			"", "eu-001.s3.synologyc2.com"),
		s3("s3-dreamhost", "Dreamhost DreamObjects", map[string]string{"provider": "Dreamhost"},
			"us-east-1", "objects.dreamhost.com"),
		s3("s3-seagate", "Seagate Lyve Cloud", map[string]string{"provider": "LyveCloud"},
			"us-east-1", "s3.us-east-1.lyvecloud.seagate.com"),
		s3("s3-ovh", "OVHcloud Object Storage", map[string]string{"provider": "Other"},
			"sbg", "s3.sbg.io.cloud.ovh.net"),
		s3("s3-qiniu", "Qiniu Kodo", map[string]string{"provider": "Qiniu"},
			"cn-east-1", "s3-cn-east-1.qiniucs.com"),
		s3("s3-arvan", "Arvan Cloud AOS", map[string]string{"provider": "ArvanCloud"},
			"ir-thr-at1", "s3.ir-thr-at1.arvanstorage.ir"),
		s3("s3-minio", "MinIO / Custom S3", map[string]string{"provider": "Minio"},
			"", ""),
		s3("s3-other", "Other S3 Compatible", map[string]string{"provider": "Other"},
			"", ""),

		//  Google

		oauth("drive", "gdrive", "Google Drive", "Google"),
		{
			Type: "gcs", RcloneType: "gcs", Name: "Google Cloud Storage", Group: "Google",
			Fields: []FormField{
				{Key: "project_number", Label: "Project Number", Placeholder: "123456789", Optional: true},
				{Key: "service_account_file", Label: "Service Account JSON", Optional: true,
					Placeholder: "C:\\path\\to\\service-account.json"},
			},
			PresetConfig: map[string]string{store.ConfigKeyOAuth: "true"},
			IsOAuth:      true,
		},
		oauth("gphotos", "gphotos", "Google Photos", "Google"),

		//  Microsoft

		oauth("onedrive", "onedrive", "Microsoft OneDrive", "Microsoft"),
		{
			Type: "azureblob", RcloneType: "azureblob", Name: "Azure Blob Storage", Group: "Microsoft",
			Fields: []FormField{
				{Key: "account", Label: "Storage Account", Placeholder: "mystorageaccount"},
				{Key: "key", Label: "Account Key", IsPassword: true, Optional: true},
				{Key: "sas_url", Label: "SAS URL", Optional: true},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "KEY"},
			PassEnvSuffix: "KEY", PasswordField: "key",
		},
		{
			Type: "azurefiles", RcloneType: "azurefiles", Name: "Azure Files Storage", Group: "Microsoft",
			Fields: []FormField{
				{Key: "account", Label: "Storage Account", Placeholder: "mystorageaccount", Row: "auth"},
				{Key: "key", Label: "Account Key", IsPassword: true, Optional: true, Row: "auth"},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "KEY"},
			PassEnvSuffix: "KEY", PasswordField: "key",
		},

		//  Other Cloud

		oauth("dropbox", "dropbox", "Dropbox", "Other Cloud"),
		oauth("box", "box", "Box", "Other Cloud"),
		oauth("pcloud", "pcloud", "pCloud", "Other Cloud"),
		oauth("yandex", "yandex", "Yandex Disk", "Other Cloud"),
		oauth("protondrive", "protondrive", "Proton Drive", "Other Cloud"),
		oauth("jottacloud", "jottacloud", "Jottacloud", "Other Cloud"),
		oauth("hidrive", "hidrive", "HiDrive", "Other Cloud"),
		oauth("zoho", "zoho", "Zoho WorkDrive", "Other Cloud"),
		oauth("putio", "putio", "put.io", "Other Cloud"),
		oauth("pikpak", "pikpak", "PikPak", "Other Cloud"),
		oauth("icloud", "icloud", "iCloud Drive", "Other Cloud"),
		oauth("filelu", "filelu", "FileLu", "Other Cloud"),
		oauth("sugarsync", "sugarsync", "SugarSync", "Other Cloud"),
		{
			Type: "mega", RcloneType: "mega", Name: "MEGA", Group: "Other Cloud",
			Fields: []FormField{
				{Key: "user", Label: "Email", Placeholder: "user@example.com", Row: "auth"},
				{Key: "pass", Label: "Password", IsPassword: true, Row: "auth"},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},
		{
			Type: "seafile", RcloneType: "seafile", Name: "Seafile", Group: "Other Cloud",
			Fields: []FormField{
				{Key: "url", Label: "Server URL", Placeholder: "https://seafile.example.com"},
				{Key: "user", Label: "Email", Placeholder: "user@example.com", Row: "auth"},
				{Key: "pass", Label: "Password", IsPassword: true, Row: "auth"},
				{Key: "library", Label: "Library Name", Optional: true},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "PASS"},
			PassEnvSuffix: "PASS", PasswordField: "pass",
		},

		//  More

		{
			Type: "b2", RcloneType: "b2", Name: "Backblaze B2 (Native)", Group: "More",
			Fields: []FormField{
				{Key: "account", Label: "Account ID", Placeholder: "your-b2-account-id", Row: "auth"},
				{Key: "key", Label: "Application Key", IsPassword: true, Row: "auth"},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "KEY"},
			PassEnvSuffix: "KEY", PasswordField: "key",
		},
		{
			Type: "koofr", RcloneType: "koofr", Name: "Koofr", Group: "More",
			Fields: []FormField{
				{Key: "user", Label: "Email", Placeholder: "user@example.com", Row: "auth"},
				{Key: "password", Label: "Password / App Password", IsPassword: true, Row: "auth"},
				{Key: "provider", Label: "Provider", Placeholder: "koofr", Default: "koofr", Optional: true},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "PASSWORD"},
			PassEnvSuffix: "PASSWORD", PasswordField: "password",
		},
		{
			Type: "storj", RcloneType: "storj", Name: "Storj (Native)", Group: "More",
			Fields: []FormField{
				{Key: "satellite_address", Label: "Satellite", Placeholder: "us1.storj.io", Default: "us1.storj.io"},
				{Key: "api_key", Label: "API Key"},
				{Key: "passphrase", Label: "Encryption Passphrase", IsPassword: true},
			},
			PresetConfig:  map[string]string{"provider": "existing", store.ConfigKeyPassEnv: "PASSPHRASE"},
			PassEnvSuffix: "PASSPHRASE", PasswordField: "passphrase",
		},
		{
			Type: "internetarchive", RcloneType: "internetarchive", Name: "Internet Archive", Group: "More",
			Fields: []FormField{
				{Key: "access_key_id", Label: "Access Key ID", Row: "auth"},
				{Key: "secret_access_key", Label: "Secret Access Key", IsPassword: true, Row: "auth"},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "SECRET_ACCESS_KEY"},
			PassEnvSuffix: "SECRET_ACCESS_KEY", PasswordField: "secret_access_key",
		},
		{
			Type: "swift", RcloneType: "swift", Name: "OpenStack Swift", Group: "More",
			Fields: []FormField{
				{Key: "user", Label: "Username", Row: "auth"},
				{Key: "key", Label: "API Key / Password", IsPassword: true, Row: "auth"},
				{Key: "auth", Label: "Auth URL", Placeholder: "https://auth.cloud.ovh.net/v3"},
				{Key: "tenant", Label: "Tenant / Project", Optional: true},
				{Key: "region", Label: "Region", Optional: true},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "KEY"},
			PassEnvSuffix: "KEY", PasswordField: "key",
		},
		{
			Type: "hdfs", RcloneType: "hdfs", Name: "HDFS", Group: "More",
			Fields: []FormField{
				{Key: "namenode", Label: "Namenode", Placeholder: "namenode:8020"},
				{Key: "username", Label: "Username", Optional: true},
			},
		},
		{
			Type: "sia", RcloneType: "sia", Name: "Sia Decentralized Cloud", Group: "More",
			Fields: []FormField{
				{Key: "api_url", Label: "API URL", Placeholder: "http://localhost:9980", Default: "http://localhost:9980"},
				{Key: "api_password", Label: "API Password", IsPassword: true, Optional: true},
			},
			PresetConfig:  map[string]string{store.ConfigKeyPassEnv: "API_PASSWORD"},
			PassEnvSuffix: "API_PASSWORD", PasswordField: "api_password",
		},
		{
			Type: "local", RcloneType: "local", Name: "Local Filesystem", Group: "More",
			// No fields needed  RemotePath is the local directory.
		},
	}
}

// FindProvider returns the ProviderDef for the given UI type key, or nil.
func FindProvider(uiType string) *ProviderDef {
	for i := range Providers {
		if Providers[i].Type == uiType {
			return &Providers[i]
		}
	}
	return nil
}

// ProviderNames returns display names in group order for use in a dropdown.
// Separators like " Group " are NOT included  the caller uses
// ProviderGroups and ProvidersByGroup to build grouped UI.
func ProviderNames() []string {
	names := make([]string, len(Providers))
	for i, p := range Providers {
		names[i] = p.Name
	}
	return names
}

// FindProviderByName returns the ProviderDef with the given display name.
func FindProviderByName(name string) *ProviderDef {
	for i := range Providers {
		if Providers[i].Name == name {
			return &Providers[i]
		}
	}
	return nil
}
