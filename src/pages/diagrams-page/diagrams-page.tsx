import React, { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button } from '@/components/button/button';
import { api, ApiClientError } from '@/api-client/client';
import type { DiagramSummary } from '@/api-client/client';
import { useAuth } from '@/hooks/use-auth';
import { SharePanel } from '@/share/share-panel';
import { VersionPanel } from '@/versions/version-panel';

export const DiagramsPage: React.FC = () => {
    const navigate = useNavigate();
    const { account, loading, logout } = useAuth();
    const [diagrams, setDiagrams] = useState<DiagramSummary[]>([]);
    const [title, setTitle] = useState('');
    const [error, setError] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);
    const [openId, setOpenId] = useState<string | null>(null);

    const reload = useCallback(async () => {
        try {
            const result = await api.listDiagrams();
            setDiagrams(result.diagrams);
        } catch (err) {
            setError(
                err instanceof ApiClientError
                    ? err.message
                    : 'Could not load diagrams.'
            );
        }
    }, []);

    useEffect(() => {
        if (!loading && !account) {
            navigate('/login', { replace: true });
        }
    }, [loading, account, navigate]);

    useEffect(() => {
        if (account) {
            reload();
        }
    }, [account, reload]);

    const create = async (event: React.FormEvent) => {
        event.preventDefault();
        setBusy(true);
        setError(null);
        try {
            await api.createDiagram(title || 'Untitled diagram', {
                tables: [],
            });
            setTitle('');
            await reload();
        } catch (err) {
            setError(
                err instanceof ApiClientError
                    ? err.message
                    : 'Could not create diagram.'
            );
        } finally {
            setBusy(false);
        }
    };

    const remove = async (id: string) => {
        setBusy(true);
        setError(null);
        try {
            await api.deleteDiagram(id);
            await reload();
        } catch (err) {
            setError(
                err instanceof ApiClientError
                    ? err.message
                    : 'Could not delete diagram.'
            );
        } finally {
            setBusy(false);
        }
    };

    const signOut = async () => {
        await logout();
        navigate('/login', { replace: true });
    };

    if (loading) {
        return <p className="p-8 text-center text-slate-500">Loading…</p>;
    }
    if (!account) {
        return null;
    }

    return (
        <section className="bg-white">
            <div className="mx-auto max-w-screen-xl px-4 py-8 lg:px-6 lg:py-16">
                <div className="mb-4 flex items-center justify-between">
                    <h1 className="text-3xl font-bold tracking-tight">
                        My diagrams
                    </h1>
                    <Button variant="outline" onClick={signOut}>
                        Log out
                    </Button>
                </div>
                <form onSubmit={create} className="mb-6 flex gap-2">
                    <input
                        type="text"
                        value={title}
                        onChange={(event) => setTitle(event.target.value)}
                        placeholder="New diagram title"
                        className="block w-full rounded-md border border-solid border-slate-300 px-3 py-2"
                    />
                    <Button type="submit" disabled={busy}>
                        Create
                    </Button>
                </form>
                {error ? (
                    <p className="mb-4 text-sm text-red-600">{error}</p>
                ) : null}
                {diagrams.length === 0 ? (
                    <p className="text-slate-500">
                        No diagrams yet — create your first one above.
                    </p>
                ) : (
                    <ul className="space-y-3">
                        {diagrams.map((diagram) => (
                            <li
                                key={diagram.id}
                                className="rounded-md border border-solid border-slate-200 p-4"
                            >
                                <div className="flex items-center justify-between">
                                    <div>
                                        <p className="font-semibold">
                                            {diagram.title}
                                        </p>
                                        <p className="text-sm text-slate-500">
                                            v{diagram.head_version} — updated{' '}
                                            {new Date(
                                                diagram.updated_at
                                            ).toLocaleString()}
                                        </p>
                                    </div>
                                    <div className="flex gap-2">
                                        <Button
                                            variant="outline"
                                            size="sm"
                                            onClick={() =>
                                                setOpenId(
                                                    openId === diagram.id
                                                        ? null
                                                        : diagram.id
                                                )
                                            }
                                        >
                                            {openId === diagram.id
                                                ? 'Hide'
                                                : 'Versions & sharing'}
                                        </Button>
                                        <Button
                                            variant="destructive"
                                            size="sm"
                                            disabled={busy}
                                            onClick={() => remove(diagram.id)}
                                        >
                                            Delete
                                        </Button>
                                    </div>
                                </div>
                                {openId === diagram.id ? (
                                    <>
                                        <VersionPanel
                                            diagramId={diagram.id}
                                            headVersion={diagram.head_version}
                                            onRestored={reload}
                                        />
                                        <SharePanel diagramId={diagram.id} />
                                    </>
                                ) : null}
                            </li>
                        ))}
                    </ul>
                )}
            </div>
        </section>
    );
};
