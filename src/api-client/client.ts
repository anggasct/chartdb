export interface ApiErrorBody {
    error: {
        code: string;
        message: string;
    };
}

export class ApiClientError extends Error {
    readonly status: number;
    readonly code: string;

    constructor(status: number, code: string, message: string) {
        super(message);
        this.status = status;
        this.code = code;
    }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
    const response = await fetch(path, {
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        ...init,
    });
    if (response.status === 204) {
        return undefined as T;
    }
    const body = (await response.json().catch(() => null)) as
        | (T & Partial<ApiErrorBody>)
        | null;
    if (!response.ok) {
        const nested = body as Partial<ApiErrorBody> | null;
        throw new ApiClientError(
            response.status,
            nested?.error?.code ?? 'request_failed',
            nested?.error?.message ?? 'Request failed.'
        );
    }
    return body as T;
}

export interface Account {
    id: string;
    display_name: string;
    primary_email: string | null;
}

export interface Identity {
    provider: string;
    email: string | null;
    email_verified: boolean;
}

export interface SessionInfo {
    id: string;
    user_agent: string | null;
    ip: string | null;
    created_at: string;
    expires_at: string;
}

export interface DiagramSummary {
    id: string;
    title: string;
    head_version: number;
    created_at: string;
    updated_at: string;
}

export interface DiagramDetail extends DiagramSummary {
    document: unknown;
}

export interface DiagramVersion {
    version: number;
    document?: unknown;
    created_by: string;
    created_at: string;
}

export interface ShareLink {
    id: string;
    expires_at: string | null;
    revoked_at: string | null;
    created_at: string;
}

export interface CreatedShare extends ShareLink {
    token: string;
}

export interface SharedDocument {
    title: string;
    document: unknown;
    version: number;
}

export interface OIDCProviderInfo {
    id: string;
    name: string;
}

export const api = {
    register: (email: string, password: string, displayName?: string) =>
        request<Account>('/api/v1/auth/register', {
            method: 'POST',
            body: JSON.stringify({
                email,
                password,
                display_name: displayName || undefined,
            }),
        }),
    login: (email: string, password: string) =>
        request<Account>('/api/v1/auth/login', {
            method: 'POST',
            body: JSON.stringify({ email, password }),
        }),
    logout: () => request<void>('/api/v1/auth/logout', { method: 'POST' }),
    me: () =>
        request<{ account: Account; identities: Identity[] }>('/api/v1/me'),
    oidcProviders: () =>
        request<{ providers: OIDCProviderInfo[] }>(
            '/api/v1/auth/oidc/providers'
        ),
    listDiagrams: (limit = 50, cursor?: string) => {
        const params = new URLSearchParams({ limit: String(limit) });
        if (cursor) {
            params.set('cursor', cursor);
        }
        return request<{ diagrams: DiagramSummary[]; next_cursor: string }>(
            `/api/v1/diagram?${params.toString()}`
        );
    },
    createDiagram: (title: string, document: unknown) =>
        request<DiagramSummary>('/api/v1/diagram', {
            method: 'POST',
            body: JSON.stringify({ title, document }),
        }),
    deleteDiagram: (id: string) =>
        request<void>(`/api/v1/diagram/${id}`, { method: 'DELETE' }),
    listVersions: (id: string) =>
        request<{ versions: DiagramVersion[] }>(
            `/api/v1/diagram/${id}/version`
        ),
    restoreVersion: (id: string, version: number) =>
        request<{ id: string; version: number; updated_at: string }>(
            `/api/v1/diagram/${id}/restore`,
            { method: 'POST', body: JSON.stringify({ version }) }
        ),
    createShare: (id: string) =>
        request<CreatedShare>(`/api/v1/diagram/${id}/share`, {
            method: 'POST',
            body: JSON.stringify({}),
        }),
    listShares: (id: string) =>
        request<{ shares: ShareLink[] }>(`/api/v1/diagram/${id}/share`),
    revokeShare: (diagramId: string, shareId: string) =>
        request<void>(`/api/v1/diagram/${diagramId}/share/${shareId}`, {
            method: 'DELETE',
        }),
    readShared: (token: string) =>
        request<SharedDocument>(`/api/v1/s/${encodeURIComponent(token)}`),
};
