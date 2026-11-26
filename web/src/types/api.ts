export type SystemStatus = {
  graydeckVersion: string;
  graydeckLatestVersion: string;
  graydeckIsLatest: boolean;
  graydeckUpdateSupported: boolean;
  deploymentMode: "docker" | "standalone";
  runtimeStatus: string;
  runtimeError: string;
  currentConfigName: string;
  runtimeMixedPort: string;
  runtimeSocksPort: string;
  runtimeRedirPort: string;
  runtimeTProxyPort: string;
  coreVersion: string;
  coreLatestVersion: string;
  coreIsLatest: boolean;
  coreExecutableReady: boolean;
  zashboardVersion: string;
  zashboardLatestVersion: string;
  zashboardIsLatest: boolean;
  zashboardReady: boolean;
  zashboardError: string;
  zashboardHideSettings: boolean;
};

export type AuthStatus = {
  enabled: boolean;
  authenticated: boolean;
  username?: string;
};

export type Subscription = {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  autoSync: boolean;
  syncInterval: string;
  lastSyncAt: string;
  lastSyncTrigger: "auto" | "manual" | "";
  lastSuccess: string;
  lastFailureReason: string;
  status: string;
  previewAvailable: boolean;
};

export type SubscriptionListResponse = {
  items: Subscription[];
};

export type SubscriptionPreview = {
  id: string;
  name: string;
  content: string;
};

export type RuntimeConfigPreview = {
  name: string;
  path: string;
  content: string;
};

export type LogEntry = {
  at: string;
  message: string;
};

export type LogListResponse = {
  items: LogEntry[];
};

export type TrafficBytes = {
  upload: number;
  download: number;
};

type TrafficPeriod = {
  id: string;
  start: string;
  end: string;
  partial: boolean;
};

export type TrafficReport = {
  currentPeriod: string;
  period: TrafficPeriod;
  periods: TrafficPeriod[];
  cycleStart: string;
  cycleEnd: string;
  periodOpen: boolean;
  retentionMonths: number;
  maxSizeMiB: number;
  timezone: string;
  sampling: boolean;
  lastSampleAt: string;
  storageError: boolean;
  totals: TrafficBytes;
  items: (TrafficBytes & { kind: "user" | "ip" | "unknown"; name: string })[];
};

export type TrafficConnection = TrafficBytes & {
  id: string;
  startedAt: string;
  lastSeenAt: string;
  sourceIP?: string;
  inboundUser?: string;
  host?: string;
  sniffHost?: string;
  destinationIP?: string;
  destinationPort?: string;
  network?: string;
  inboundType?: string;
  inboundName?: string;
  node?: string;
  route?: string[];
  rule?: string;
  rulePayload?: string;
};

export type TrafficConnectionReport = {
  retentionDays: number;
  maxSizeMiB: number;
  maxRecords: number;
  storageError: boolean;
  nextCursor?: string;
  items: TrafficConnection[];
};
