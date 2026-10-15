import React, { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { api, ApiClientError } from '@/api-client/client';
import type { SharedDocument } from '@/api-client/client';

export const SharedViewPage: React.FC = () => {
    const { token } = useParams<{ token: string }>();
    const [state, setState] = useState<
        | { kind: 'loading' }
        | { kind: 'ready'; document: SharedDocument }
        | { kind: 'invalid' }
    >({ kind: 'loading' });

    useEffect(() => {
        if (!token) {
            setState({ kind: 'invalid' });
            return;
        }
        api.readShared(token)
            .then((document) => {
                setState({ kind: 'ready', document });
            })
            .catch((err) => {
                if (err instanceof ApiClientError && err.status === 404) {
                    setState({ kind: 'invalid' });
                    return;
                }
                setState({ kind: 'invalid' });
            });
    }, [token]);

    if (state.kind === 'loading') {
        return <p className="p-8 text-center text-slate-500">Loading…</p>;
    }
    if (state.kind === 'invalid') {
        return (
            <section className="bg-white">
                <div className="mx-auto max-w-screen-sm px-4 py-8 text-center lg:py-16">
                    <h1 className="mb-4 text-3xl font-bold tracking-tight">
                        Link unavailable
                    </h1>
                    <p className="text-slate-500">
                        This share link is invalid, expired, or revoked.
                    </p>
                </div>
            </section>
        );
    }
    return (
        <section className="bg-white">
            <div className="mx-auto max-w-screen-xl px-4 py-8 lg:px-6 lg:py-16">
                <p className="mb-2 inline-block rounded bg-slate-100 px-2 py-1 text-xs text-slate-600">
                    Read-only shared view (v{state.document.version})
                </p>
                <h1 className="mb-4 text-3xl font-bold tracking-tight">
                    {state.document.title}
                </h1>
                <pre className="overflow-auto rounded-md bg-slate-50 p-4 text-sm">
                    {JSON.stringify(state.document.document, null, 2)}
                </pre>
            </div>
        </section>
    );
};
