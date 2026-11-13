export namespace config {
	
	export class HistoryRow {
	    name: string;
	    category: string;
	    avgMs: number;
	    p95Ms: number;
	    jitterMs: number;
	    lossPct: number;
	
	    static createFrom(source: any = {}) {
	        return new HistoryRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.category = source["category"];
	        this.avgMs = source["avgMs"];
	        this.p95Ms = source["p95Ms"];
	        this.jitterMs = source["jitterMs"];
	        this.lossPct = source["lossPct"];
	    }
	}
	export class HistoryEntry {
	    id: string;
	    timestamp: any;
	    adapterName: string;
	    systemDns?: string[];
	    durationMs: number;
	    winnerId?: string;
	    winnerName?: string;
	    winnerAvgMs?: number;
	    currentAvgMs?: number;
	    improvementPct?: number;
	    intercepted: boolean;
	    topResults: HistoryRow[];
	
	    static createFrom(source: any = {}) {
	        return new HistoryEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.timestamp = source["timestamp"];
	        this.adapterName = source["adapterName"];
	        this.systemDns = source["systemDns"];
	        this.durationMs = source["durationMs"];
	        this.winnerId = source["winnerId"];
	        this.winnerName = source["winnerName"];
	        this.winnerAvgMs = source["winnerAvgMs"];
	        this.currentAvgMs = source["currentAvgMs"];
	        this.improvementPct = source["improvementPct"];
	        this.intercepted = source["intercepted"];
	        this.topResults = this.convertValues(source["topResults"], HistoryRow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Settings {
	    viewMode: string;
	    categoryFilter: string;
	    sortBy: string;
	    options: dns.Options;
	    autoCheckUpdates: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.viewMode = source["viewMode"];
	        this.categoryFilter = source["categoryFilter"];
	        this.sortBy = source["sortBy"];
	        this.options = this.convertValues(source["options"], dns.Options);
	        this.autoCheckUpdates = source["autoCheckUpdates"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace dns {
	
	export class FilteringResult {
	    checked: boolean;
	    blocksAds: boolean;
	    blockedCount: number;
	    testedCount: number;
	    blockedHosts?: string[];
	    method?: string;
	
	    static createFrom(source: any = {}) {
	        return new FilteringResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.checked = source["checked"];
	        this.blocksAds = source["blocksAds"];
	        this.blockedCount = source["blockedCount"];
	        this.testedCount = source["testedCount"];
	        this.blockedHosts = source["blockedHosts"];
	        this.method = source["method"];
	    }
	}
	export class HijackResult {
	    status: string;
	    findings?: string[];
	    udpEgress?: string;
	    dohEgress?: string;
	
	    static createFrom(source: any = {}) {
	        return new HijackResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.findings = source["findings"];
	        this.udpEgress = source["udpEgress"];
	        this.dohEgress = source["dohEgress"];
	    }
	}
	export class NetworkIntegrity {
	    checked: boolean;
	    intercepted: boolean;
	    probeTarget: string;
	    hijackedCount: number;
	    summary: string;
	
	    static createFrom(source: any = {}) {
	        return new NetworkIntegrity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.checked = source["checked"];
	        this.intercepted = source["intercepted"];
	        this.probeTarget = source["probeTarget"];
	        this.hijackedCount = source["hijackedCount"];
	        this.summary = source["summary"];
	    }
	}
	export class Options {
	    queries: number;
	    timeoutMs: number;
	    workers: number;
	    staggerMs: number;
	    protocols: string[];
	    cachedDomains: string[];
	    uncachedBase: string;
	    checkIntegrity: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Options(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.queries = source["queries"];
	        this.timeoutMs = source["timeoutMs"];
	        this.workers = source["workers"];
	        this.staggerMs = source["staggerMs"];
	        this.protocols = source["protocols"];
	        this.cachedDomains = source["cachedDomains"];
	        this.uncachedBase = source["uncachedBase"];
	        this.checkIntegrity = source["checkIntegrity"];
	    }
	}
	export class Stats {
	    sent: number;
	    received: number;
	    avgMs: number;
	    minMs: number;
	    maxMs: number;
	    medianMs: number;
	    p95Ms: number;
	    jitterMs: number;
	    lossPct: number;
	    samples?: number[];
	
	    static createFrom(source: any = {}) {
	        return new Stats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sent = source["sent"];
	        this.received = source["received"];
	        this.avgMs = source["avgMs"];
	        this.minMs = source["minMs"];
	        this.maxMs = source["maxMs"];
	        this.medianMs = source["medianMs"];
	        this.p95Ms = source["p95Ms"];
	        this.jitterMs = source["jitterMs"];
	        this.lossPct = source["lossPct"];
	        this.samples = source["samples"];
	    }
	}
	export class ProtocolResult {
	    protocol: string;
	    endpoint: string;
	    cached: Stats;
	    uncached: Stats;
	    combined: Stats;
	    failures?: Record<string, number>;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProtocolResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.protocol = source["protocol"];
	        this.endpoint = source["endpoint"];
	        this.cached = this.convertValues(source["cached"], Stats);
	        this.uncached = this.convertValues(source["uncached"], Stats);
	        this.combined = this.convertValues(source["combined"], Stats);
	        this.failures = source["failures"];
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Recommendation {
	    resolverId: string;
	    name: string;
	    avgMs: number;
	    currentAvgMs: number;
	    improvementPct: number;
	    currentIsFastest: boolean;
	    headline: string;
	    detail: string;
	
	    static createFrom(source: any = {}) {
	        return new Recommendation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.resolverId = source["resolverId"];
	        this.name = source["name"];
	        this.avgMs = source["avgMs"];
	        this.currentAvgMs = source["currentAvgMs"];
	        this.improvementPct = source["improvementPct"];
	        this.currentIsFastest = source["currentIsFastest"];
	        this.headline = source["headline"];
	        this.detail = source["detail"];
	    }
	}
	export class Resolver {
	    id: string;
	    name: string;
	    category: string;
	    source: string;
	    description?: string;
	    ipv4?: string[];
	    ipv6?: string[];
	    doh?: string;
	    dot?: string;
	    deprecated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Resolver(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.category = source["category"];
	        this.source = source["source"];
	        this.description = source["description"];
	        this.ipv4 = source["ipv4"];
	        this.ipv6 = source["ipv6"];
	        this.doh = source["doh"];
	        this.dot = source["dot"];
	        this.deprecated = source["deprecated"];
	    }
	}
	export class ServerResult {
	    resolver: Resolver;
	    rank: number;
	    status: string;
	    score: number;
	    primary: string;
	    overall: Stats;
	    cached: Stats;
	    uncached: Stats;
	    protocols: Record<string, ProtocolResult>;
	    dnssec: string;
	    filtering: FilteringResult;
	    hijack: HijackResult;
	
	    static createFrom(source: any = {}) {
	        return new ServerResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.resolver = this.convertValues(source["resolver"], Resolver);
	        this.rank = source["rank"];
	        this.status = source["status"];
	        this.score = source["score"];
	        this.primary = source["primary"];
	        this.overall = this.convertValues(source["overall"], Stats);
	        this.cached = this.convertValues(source["cached"], Stats);
	        this.uncached = this.convertValues(source["uncached"], Stats);
	        this.protocols = this.convertValues(source["protocols"], ProtocolResult, true);
	        this.dnssec = source["dnssec"];
	        this.filtering = this.convertValues(source["filtering"], FilteringResult);
	        this.hijack = this.convertValues(source["hijack"], HijackResult);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Report {
	    version: string;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    finishedAt: any;
	    durationMs: number;
	    options: Options;
	    results: ServerResult[];
	    recommendation?: Recommendation;
	    network: NetworkIntegrity;
	    cancelled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Report(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.durationMs = source["durationMs"];
	        this.options = this.convertValues(source["options"], Options);
	        this.results = this.convertValues(source["results"], ServerResult);
	        this.recommendation = this.convertValues(source["recommendation"], Recommendation);
	        this.network = this.convertValues(source["network"], NetworkIntegrity);
	        this.cancelled = source["cancelled"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	

}

export namespace main {
	
	export class ActionResult {
	    success: boolean;
	    message: string;
	    needsManual?: boolean;
	    cancelled?: boolean;
	    addresses?: string[];
	    manual?: sysnet.ManualGuide;
	    network?: sysnet.Info;
	
	    static createFrom(source: any = {}) {
	        return new ActionResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.needsManual = source["needsManual"];
	        this.cancelled = source["cancelled"];
	        this.addresses = source["addresses"];
	        this.manual = this.convertValues(source["manual"], sysnet.ManualGuide);
	        this.network = this.convertValues(source["network"], sysnet.Info);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Bootstrap {
	    version: string;
	    repoUrl: string;
	    license: string;
	    configDir?: string;
	    network?: sysnet.Info;
	    resolvers: dns.Resolver[];
	    defaultOptions: dns.Options;
	    settings: config.Settings;
	    lastReport?: dns.Report;
	
	    static createFrom(source: any = {}) {
	        return new Bootstrap(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.repoUrl = source["repoUrl"];
	        this.license = source["license"];
	        this.configDir = source["configDir"];
	        this.network = this.convertValues(source["network"], sysnet.Info);
	        this.resolvers = this.convertValues(source["resolvers"], dns.Resolver);
	        this.defaultOptions = this.convertValues(source["defaultOptions"], dns.Options);
	        this.settings = this.convertValues(source["settings"], config.Settings);
	        this.lastReport = this.convertValues(source["lastReport"], dns.Report);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class VerifyResult {
	    success: boolean;
	    message: string;
	    resolverName: string;
	    addresses?: string[];
	    avgMs: number;
	    cachedAvgMs: number;
	    uncachedAvgMs: number;
	    p95Ms: number;
	    lossPct: number;
	    previousAvgMs?: number;
	    improvementPct?: number;
	
	    static createFrom(source: any = {}) {
	        return new VerifyResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.resolverName = source["resolverName"];
	        this.addresses = source["addresses"];
	        this.avgMs = source["avgMs"];
	        this.cachedAvgMs = source["cachedAvgMs"];
	        this.uncachedAvgMs = source["uncachedAvgMs"];
	        this.p95Ms = source["p95Ms"];
	        this.lossPct = source["lossPct"];
	        this.previousAvgMs = source["previousAvgMs"];
	        this.improvementPct = source["improvementPct"];
	    }
	}

}

export namespace updater {
	
	export class ReleaseInfo {
	    currentVersion: string;
	    version: string;
	    tagName: string;
	    title: string;
	    body: string;
	    publishedAt: string;
	    htmlUrl: string;
	    assetUrl?: string;
	    assetName?: string;
	    assetSize?: number;
	    sha256?: string;
	    isAvailable: boolean;
	    assetReady: boolean;
	    canSelfUpdate: boolean;
	    updateNote?: string;
	
	    static createFrom(source: any = {}) {
	        return new ReleaseInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.currentVersion = source["currentVersion"];
	        this.version = source["version"];
	        this.tagName = source["tagName"];
	        this.title = source["title"];
	        this.body = source["body"];
	        this.publishedAt = source["publishedAt"];
	        this.htmlUrl = source["htmlUrl"];
	        this.assetUrl = source["assetUrl"];
	        this.assetName = source["assetName"];
	        this.assetSize = source["assetSize"];
	        this.sha256 = source["sha256"];
	        this.isAvailable = source["isAvailable"];
	        this.assetReady = source["assetReady"];
	        this.canSelfUpdate = source["canSelfUpdate"];
	        this.updateNote = source["updateNote"];
	    }
	}

}

export namespace sysnet {
	
	export class Adapter {
	    id: string;
	    name: string;
	    device: string;
	    kind: string;
	    mac?: string;
	    ipv4: string[];
	    ipv6: string[];
	    gateway?: string;
	    dns: string[];
	    dnsManual: boolean;
	    connection?: string;
	    hasIPv6: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Adapter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.device = source["device"];
	        this.kind = source["kind"];
	        this.mac = source["mac"];
	        this.ipv4 = source["ipv4"];
	        this.ipv6 = source["ipv6"];
	        this.gateway = source["gateway"];
	        this.dns = source["dns"];
	        this.dnsManual = source["dnsManual"];
	        this.connection = source["connection"];
	        this.hasIPv6 = source["hasIPv6"];
	    }
	}
	export class Info {
	    os: string;
	    osName: string;
	    arch: string;
	    hostname: string;
	    elevated: boolean;
	    online: boolean;
	    backend: string;
	    active?: Adapter;
	    interfaces: Adapter[];
	    systemDns: string[];
	    gateway: string;
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.os = source["os"];
	        this.osName = source["osName"];
	        this.arch = source["arch"];
	        this.hostname = source["hostname"];
	        this.elevated = source["elevated"];
	        this.online = source["online"];
	        this.backend = source["backend"];
	        this.active = this.convertValues(source["active"], Adapter);
	        this.interfaces = this.convertValues(source["interfaces"], Adapter);
	        this.systemDns = source["systemDns"];
	        this.gateway = source["gateway"];
	        this.warnings = source["warnings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ManualGuide {
	    steps: string[];
	    command: string;
	
	    static createFrom(source: any = {}) {
	        return new ManualGuide(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.steps = source["steps"];
	        this.command = source["command"];
	    }
	}

}

