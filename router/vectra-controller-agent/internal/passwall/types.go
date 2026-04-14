package passwall

type DesiredConfig struct {
	SchemaVersion int                  `json:"schemaVersion,omitempty"`
	BasicSettings BasicSettingsConfig  `json:"basicSettings"`
	Nodes         []NodeConfig         `json:"nodes,omitempty"`
	Subscriptions SubscriptionSettings `json:"subscriptions"`
	AppUpdate     AppUpdateConfig      `json:"appUpdate"`
	RuleManage    RuleManageConfig     `json:"ruleManage"`
}

type BasicSettingsConfig struct {
	Main        MainSettings        `json:"main"`
	DNS         DNSSettings         `json:"dns"`
	Log         LogSettings         `json:"log"`
	Maintenance MaintenanceSettings `json:"maintenance"`
	Socks       []SocksConfig       `json:"socks,omitempty"`
	ShuntRules  []ShuntRule         `json:"shuntRules,omitempty"`
}

type MainSettings struct {
	MainSwitch         bool           `json:"mainSwitch"`
	SelectedNodeID     string         `json:"selectedNodeId,omitempty"`
	LocalhostProxy     bool           `json:"localhostProxy"`
	ClientProxy        bool           `json:"clientProxy"`
	NodeSocksPort      int            `json:"nodeSocksPort,omitempty"`
	NodeSocksBindLocal bool           `json:"nodeSocksBindLocal"`
	SocksMainSwitch    bool           `json:"socksMainSwitch"`
	Extras             map[string]any `json:"extras,omitempty"`
}

type DNSSettings struct {
	DirectQueryStrategy    string         `json:"directQueryStrategy,omitempty"`
	RemoteDNSProtocol      string         `json:"remoteDnsProtocol,omitempty"`
	RemoteDNS              string         `json:"remoteDns,omitempty"`
	RemoteDNSDOH           string         `json:"remoteDnsDoh,omitempty"`
	RemoteDNSClientIP      string         `json:"remoteDnsClientIp,omitempty"`
	RemoteDNSDetour        string         `json:"remoteDnsDetour,omitempty"`
	RemoteFakeDNS          bool           `json:"remoteFakeDns"`
	RemoteDNSQueryStrategy string         `json:"remoteDnsQueryStrategy,omitempty"`
	DNSHosts               []string       `json:"dnsHosts,omitempty"`
	DNSRedirect            bool           `json:"dnsRedirect"`
	Extras                 map[string]any `json:"extras,omitempty"`
}

type LogSettings struct {
	EnableNodeLog bool           `json:"enableNodeLog"`
	Level         string         `json:"level,omitempty"`
	Extras        map[string]any `json:"extras,omitempty"`
}

type MaintenanceSettings struct {
	BackupPaths []string       `json:"backupPaths,omitempty"`
	Extras      map[string]any `json:"extras,omitempty"`
}

type SocksConfig struct {
	ID                      string         `json:"id"`
	Enabled                 bool           `json:"enabled"`
	NodeID                  string         `json:"nodeId,omitempty"`
	Port                    int            `json:"port,omitempty"`
	HTTPPort                int            `json:"httpPort,omitempty"`
	BindLocal               bool           `json:"bindLocal"`
	AutoswitchBackupNodeIDs []string       `json:"autoswitchBackupNodeIds,omitempty"`
	Extras                  map[string]any `json:"extras,omitempty"`
}

type ShuntRule struct {
	ID             string         `json:"id"`
	Label          string         `json:"label"`
	OutboundNodeID string         `json:"outboundNodeId,omitempty"`
	DomainRules    []string       `json:"domainRules,omitempty"`
	IPRules        []string       `json:"ipRules,omitempty"`
	Extras         map[string]any `json:"extras,omitempty"`
}

type NodeConfig struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Protocol  string         `json:"protocol"`
	Enabled   bool           `json:"enabled"`
	Group     string         `json:"group,omitempty"`
	Address   string         `json:"address,omitempty"`
	Port      int            `json:"port,omitempty"`
	Username  string         `json:"username,omitempty"`
	Password  string         `json:"password,omitempty"`
	Transport string         `json:"transport,omitempty"`
	TLS       *bool          `json:"tls,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
	Extras    map[string]any `json:"extras,omitempty"`
}

type SubscriptionSettings struct {
	FilterKeywordMode string              `json:"filterKeywordMode,omitempty"`
	DiscardList       []string            `json:"discardList,omitempty"`
	KeepList          []string            `json:"keepList,omitempty"`
	TypePreferences   SubscriptionTypes   `json:"typePreferences"`
	DomainStrategy    string              `json:"domainStrategy,omitempty"`
	Items             []SubscriptionEntry `json:"items,omitempty"`
}

type SubscriptionTypes struct {
	Shadowsocks string `json:"shadowsocks,omitempty"`
	Trojan      string `json:"trojan,omitempty"`
	Vmess       string `json:"vmess,omitempty"`
	Vless       string `json:"vless,omitempty"`
	Hysteria2   string `json:"hysteria2,omitempty"`
}

type SubscriptionEntry struct {
	ID       string               `json:"id"`
	Remark   string               `json:"remark"`
	URL      string               `json:"url,omitempty"`
	Enabled  bool                 `json:"enabled"`
	AddMode  string               `json:"addMode,omitempty"`
	Metadata SubscriptionMetadata `json:"metadata"`
	Extras   map[string]any       `json:"extras,omitempty"`
}

type SubscriptionMetadata struct {
	RemainingTraffic string `json:"remainingTraffic,omitempty"`
	ExpiresAt        string `json:"expiresAt,omitempty"`
}

type AppUpdateConfig struct {
	BinaryPaths    BinaryPathConfig    `json:"binaryPaths"`
	UpdateStrategy string              `json:"updateStrategy,omitempty"`
	TargetVersions TargetVersionConfig `json:"targetVersions"`
	Extras         map[string]any      `json:"extras,omitempty"`
}

type BinaryPathConfig struct {
	Xray     string `json:"xray,omitempty"`
	SingBox  string `json:"singBox,omitempty"`
	Hysteria string `json:"hysteria,omitempty"`
	Geoview  string `json:"geoview,omitempty"`
}

type TargetVersionConfig struct {
	AppVersion string `json:"appVersion,omitempty"`
	Xray       string `json:"xray,omitempty"`
	SingBox    string `json:"singBox,omitempty"`
	Hysteria   string `json:"hysteria,omitempty"`
	Geoview    string `json:"geoview,omitempty"`
}

type RuleManageConfig struct {
	GeoIPURL       string         `json:"geoipUrl,omitempty"`
	GeoSiteURL     string         `json:"geositeUrl,omitempty"`
	AssetDirectory string         `json:"assetDirectory,omitempty"`
	AutoUpdate     bool           `json:"autoUpdate"`
	ScheduleMode   string         `json:"scheduleMode,omitempty"`
	ScheduleDay    int            `json:"scheduleDay,omitempty"`
	ScheduleHour   int            `json:"scheduleHour,omitempty"`
	IntervalHours  int            `json:"intervalHours,omitempty"`
	EnabledAssets  []string       `json:"enabledAssets,omitempty"`
	ShuntRules     []ShuntRule    `json:"shuntRules,omitempty"`
	Extras         map[string]any `json:"extras,omitempty"`
}

type ImportedState struct {
	Config       DesiredConfig  `json:"config"`
	RawSnapshot  map[string]any `json:"rawSnapshot,omitempty"`
	ConfigDigest string         `json:"configDigest"`
	ImportedAt   string         `json:"importedAt,omitempty"`
	Source       string         `json:"source,omitempty"`
}

type CommandSpec struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

type Operation struct {
	Kind            string        `json:"kind"`
	Section         string        `json:"section,omitempty"`
	Description     string        `json:"description"`
	RestartRequired bool          `json:"restartRequired,omitempty"`
	UCICommands     []string      `json:"uciCommands,omitempty"`
	Commands        []CommandSpec `json:"commands,omitempty"`
}

type ApplyPlan struct {
	Operations           []Operation `json:"operations"`
	RequiresRestart      bool        `json:"requiresRestart"`
	RefreshSubscriptions bool        `json:"refreshSubscriptions"`
	RefreshRules         bool        `json:"refreshRules"`
	PackageInstall       bool        `json:"packageInstall"`
}

type ApplyOptions struct {
	RefreshSubscriptions bool
	RefreshRules         bool
	RestartService       bool
}

type CommandResult struct {
	Command string `json:"command"`
	Stdout  string `json:"stdout,omitempty"`
	Stderr  string `json:"stderr,omitempty"`
}

type ApplyResult struct {
	Plan           ApplyPlan       `json:"plan"`
	ConfigDigest   string          `json:"configDigest"`
	UCICommands    []string        `json:"uciCommands,omitempty"`
	CommandResults []CommandResult `json:"commandResults,omitempty"`
}
