import React, { useCallback, useEffect, useState } from 'react';
import { Button } from '@/components/button/button';
import { api, ApiClientError } from '@/api-client/client';
import type { ShareLink } from '@/api-client/client';

export const SharePanel: React.FC<{ diagramId: string }> = ({ diagramId }) => {
    const [links, setLinks] = useState<ShareLink[]>([]);
    const [latestToken, setLatestToken] = useState<string | null>(null);
    const [error, setError] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);

    const reload = useCallback(async () => {
        try {
            const result = await api.listShares(diagramId);
            setLinks(result.shares);
        } catch (err) {
            setError(
                err instanceof ApiClientError
                    ? err.message
                    : 'Could not load share links.'
            );
        }
    }, [diagramId]);

    useEffect(() => {
        reload();
    }, [reload]);

    const create = async () => {
        setBusy(true);
        setError(null);
        try {
            const link = await api.createShare(diagramId);
            setLatestToken(link.token);
            await reload();
        } catch (err) {
            setError(
                err instanceof ApiClientError
                    ? err.message
                    : 'Could not create share link.'
            );
        } finally {
            setBusy(false);
        }
    };

    const revoke = async (shareId: string) => {
        setBusy(true);
        setError(null);
        try {
            await api.revokeShare(diagramId, shareId);
            await reload();
        } catch (err) {
            setError(
                err instanceof ApiClientError
                    ? err.message
                    : 'Could not revoke share link.'
            );
        } finally {
            setBusy(false);
        }
    };

    return (
        <div className="mt-2 rounded-md border border-solid border-slate-200 p-3">
            <div className="flex items-center justify-between">
                <h4 className="text-sm font-semibold">Share links</h4>
                <Button size="sm" onClick={create} disabled={busy}>
                    New link
                </Button>
            </div>
            {error ? (
                <p className="mt-2 text-sm text-red-600">{error}</p>
            ) : null}
            {latestToken ? (
                <p className="mt-2 break-all text-sm">
                    New link (copy now, shown once):{' '}
                    <code>{`${window.location.origin}/s/${latestToken}`}</code>
                </p>
            ) : null}
            {links.length === 0 ? (
                <p className="mt-2 text-sm text-slate-500">
                    No share links yet.
                </p>
            ) : (
                <ul className="mt-2 space-y-1">
                    {links.map((link) => (
                        <li
                            key={link.id}
                            className="flex items-center justify-between text-sm"
                        >
                            <span className="font-mono text-xs">
                                {link.id.slice(0, 8)}…
                                {link.revoked_at ? ' (revoked)' : ''}
                            </span>
                            {!link.revoked_at ? (
                                <Button
                                    size="sm"
                                    variant="outline"
                                    onClick={() => revoke(link.id)}
                                    disabled={busy}
                                >
                                    Revoke
                                </Button>
                            ) : null}
                        </li>
                    ))}
                </ul>
            )}
        </div>
    );
};
