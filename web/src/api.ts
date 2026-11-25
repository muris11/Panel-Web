/** The panel's API client. One function, one error shape. */

export type Role = 'admin' | 'reseller' | 'user';
export type Status = 'active' | 'pending' | 'suspended' | 'deleted';

export interface User {
    id: number;
    username: string;
    name: string;
    email: string;
    role: Role;
    status: Status;
    twoFactor: boolean;
    lastLoginAt: string | null;
}

export interface Package {
    id: number;
    name: string;
    description: string;
    diskMb: number | null;
    bandwidthMb: number | null;
    domains: number | null;
    databases: number | null;
    emailAccounts: number | null;
    ftpAccounts?: number | null;
    cronJobs?: number | null;
    subdomains?: number | null;
}

export interface Account {
    id: number;
    username: string;
    name: string;
    email: string;
    status: Status;
    reason: string;
    owner: string;
    uid: number | null;
    package: Package | null;
    domains: number;
    createdAt: string;
    lastLoginAt: string | null;
}

export interface Domain {
    id: number;
    name: string;
    type: string;
    phpVersion: string | null;
    isPrimary: boolean;
    documentRoot: string | null;
    status: string;
    sslStatus: string;
    createdAt: string;
}

export interface Task {
    id: string;
    action: string;
    username: string | null;
    status: 'queued' | 'running' | 'success' | 'failed' | 'cancelled';
    error: string | null;
    output?: string | null;
    attempts?: number;
    createdAt: string;
    startedAt?: string | null;
    finishedAt: string | null;
}

export interface Stats {
    accounts: number;
    active: number;
    suspended: number;
    pending: number;
    domains: number;
    resellers: number;
    tasksFailed: number;
    tasksQueued: number;
}

export interface SystemInfo {
    hostname: string;
    os: { name: string; version: string; kernel: string };
    cpu: { model: string; cores: number };
    load: number[];
    memory: { total_mb: number; used_mb: number };
    disk: { mount: string; total_gb: number; used_gb: number };
    uptime_seconds: number;
    agent_version: string;
    simulated?: boolean;
}

export interface SessionRow {
    id: string;
    ip: string;
    userAgent: string;
    current: boolean;
    createdAt: string;
    lastSeenAt: string;
}

export class ApiError extends Error {
    readonly code: string;
    readonly status: number;
    readonly fields: Record<string, string>;

    constructor(
        message: string,
        code: string,
        status: number,
        fields: Record<string, string> = {},
    ) {
        super(message);
        this.name = 'ApiError';
        this.code = code;
        this.status = status;
        this.fields = fields;
    }
}

let csrfToken = '';

export function setCSRFToken(token: string): void {
    csrfToken = token;
}

export function currentCSRFToken(): string {
    return csrfToken;
}

interface RequestOptions {
    method?: 'GET' | 'POST' | 'DELETE' | 'PATCH' | 'PUT';
    body?: unknown;
    /** Set for calls made before a session exists, which use the pre-session
     *  token from the cookie instead of the in-memory one. */
    csrf?: string;
}

export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const method = options.method ?? 'GET';

    const headers: Record<string, string> = { Accept: 'application/json' };

    if (options.body !== undefined) {
        headers['Content-Type'] = 'application/json';
    }

    if (method !== 'GET') {
        headers['X-CSRF-Token'] = options.csrf ?? csrfToken;
    }

    const response = await fetch(`/api/v1${path}`, {
        method,
        headers,
        credentials: 'same-origin',
        body: options.body === undefined ? undefined : JSON.stringify(options.body),
    });

    if (response.status === 204) {
        return undefined as T;
    }

    const text = await response.text();
    const payload: unknown = text ? safeJSON(text) : null;

    if (response.status === 401 && !path.startsWith('/auth/')) {
        // The session expired or was ended elsewhere. The panel has one place
        // that reacts to this, so every screen gets the sign-in form back
        // instead of a banner it cannot clear.
        window.dispatchEvent(new Event('muriscore:signed-out'));
    }

    if (!response.ok) {
        const envelope = payload as
            | { error?: { code?: string; message?: string; fields?: Record<string, string> } }
            | null;

        throw new ApiError(
            envelope?.error?.message ?? `The panel answered ${response.status}.`,
            envelope?.error?.code ?? 'http_error',
            response.status,
            envelope?.error?.fields ?? {},
        );
    }

    return payload as T;
}

function safeJSON(text: string): unknown {
    try {
        return JSON.parse(text);
    } catch {
        return null;
    }
}

/** Formats an ISO timestamp the way the panel shows dates. */
export function when(value: string | null | undefined): string {
    if (!value) {
        return '—';
    }

    const date = new Date(value);

    if (Number.isNaN(date.getTime())) {
        return '—';
    }

    const now = Date.now();
    const diff = Math.round((now - date.getTime()) / 1000);

    if (diff >= 0 && diff < 60) {
        return 'just now';
    }

    if (diff >= 0 && diff < 3600) {
        return `${Math.floor(diff / 60)} min ago`;
    }

    if (diff >= 0 && diff < 86400) {
        return `${Math.floor(diff / 3600)} h ago`;
    }

    return new Intl.DateTimeFormat('en-GB', {
        day: '2-digit',
        month: 'short',
        year: date.getFullYear() === new Date().getFullYear() ? undefined : 'numeric',
        hour: '2-digit',
        minute: '2-digit',
    }).format(date);
}

/** Turns bytes or megabytes into the shortest readable form. */
export function megabytes(value: number | null | undefined): string {
    if (value === null || value === undefined) {
        return '—';
    }

    if (value >= 1024 * 1024) {
        return `${(value / (1024 * 1024)).toFixed(1)} TB`;
    }

    if (value >= 1024) {
        return `${(value / 1024).toFixed(value >= 10240 ? 0 : 1)} GB`;
    }

    return `${Math.round(value)} MB`;
}

/** "account.suspend" reads better as "Account suspend". */
export function actionLabel(action: string): string {
    const [group = '', verb = ''] = action.split('.');
    const words = `${group} ${verb}`.replace(/_/g, ' ').trim().toLowerCase();

    if (!words) {
        return action;
    }

    return words.charAt(0).toUpperCase() + words.slice(1);
}

export interface DNSRecord {
    id: number;
    zoneId: number;
    name: string;
    type: string;
    content: string;
    ttl: number;
    priority: number | null;
}

export interface Zone {
    id: number;
    name: string;
    domainId: number | null;
    records?: DNSRecord[];
}

export interface DomainDetail {
    id: number;
    name: string;
    type: string;
    username: string;
    phpVersion: string | null;
    documentRoot: string | null;
    isPrimary: boolean;
    status: string;
    sslStatus: string;
    sslExpiresAt?: string | null;
    createdAt: string;
    publicIp: string;
    nameservers: string[] | null;
    zone: Zone | null;
}

export interface MailDomain {
    id: number;
    domainId: number;
    domainName: string;
    username: string;
    dkimSelector: string;
    dkimPublicKey: string;
    catchAll: string;
    status: string;
    sslStatus: string;
    mailboxes: number;
    forwarders: number;
}

export interface Mailbox {
    id: number;
    mailDomainId: number;
    localPart: string;
    quotaMb: number | null;
    dailySendLimit: number | null;
    status: string;
}

export interface Forwarder {
    id: number;
    mailDomainId: number;
    localPart: string;
    destinations: string[];
}

export interface DatabaseRow {
    id: number;
    userId: number;
    name: string;
    status: string;
    sizeBytes: number | null;
    username: string;
}

export interface DatabaseUserRow {
    id: number;
    userId: number;
    name: string;
    status: string;
    username: string;
    databases: string[];
}

export interface PostgresDatabase {
    id: number;
    userId: number;
    name: string;
    status: string;
    sizeBytes: number | null;
    username: string;
}

export interface PostgresLogin {
    id: number;
    userId: number;
    name: string;
    status: string;
    username: string;
    databases: string[];
}

export interface RedisInstance {
    id: number;
    userId: number;
    maxMemoryMb: number;
    enabled: boolean;
    username: string;
    host: string;
    port: number;
}

export interface FTPLogin {
    id: number;
    userId: number;
    domainId: number;
    login: string;
    directory: string;
    quotaMb: number | null;
    status: string;
    username: string;
    domainName: string;
}

export interface CronJobRow {
    id: number;
    userId: number;
    schedule: string;
    command: string;
    description: string;
    enabled: boolean;
}

export interface FileEntry {
    name: string;
    type: string;
    size: number;
    mode: string;
    modified: number;
    target?: string;
}

export interface Listing {
    path: string;
    entries: FileEntry[];
    truncated: boolean;
}

export interface Content {
    path?: string;
    content: string;
    size: number;
    version: string;
}

export interface TokenRow {
    id: number;
    name: string;
    lastUsedAt: string | null;
    expiresAt: string | null;
    createdAt: string;
}

export interface ServiceRow {
    unit: string;
    label: string;
    state: string;
    enabled: boolean;
}

export interface ServerPage {
    services: { services: ServiceRow[] } | null;
    firewall: { enabled: boolean; rules: { port: number; protocol: string; action: string }[] } | null;
    settings: Record<string, unknown> | null;
    bfm: Record<string, unknown> | null;
    problems: Record<string, string>;
}

export interface QueueAnswer {
    queued: boolean;
    action: string;
    task?: string;
}
