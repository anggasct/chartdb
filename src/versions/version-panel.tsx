import React, { useCallback, useEffect, useState } from 'react';
import { Button } from '@/components/button/button';
import { api, ApiClientError } from '@/api-client/client';
import type { DiagramVersion } from '@/api-client/client';

export const VersionPanel: React.FC<{
    diagramId: string;
    headVersion: number;
    onRestored: () => void;
}> = ({ diagramId, headVersion, onRestored }) => {
    const [versions, setVersions] = useState<DiagramVersion[]>([]);
    const [error, setError] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);

    const reload = useCallback(async () => {
        try {
            const result = await api.listVersions(diagramId);
            setVersions(result.versions);
        } catch (err) {
            setError(
                err instanceof ApiClientError
                    ? err.message
                    : 'Could not load versions.'
            );
        }
    }, [diagramId]);

    useEffect(() => {
        reload();
    }, [reload]);

    const restore = async (version: number) => {
        setBusy(true);
        setError(null);
        try {
            await api.restoreVersion(diagramId, version);
            onRestored();
        } catch (err) {
            setError(
                err instanceof ApiClientError
                    ? err.message
                    : 'Could not restore version.'
            );
        } finally {
            setBusy(false);
        }
    };

    return (
        <div className="mt-2 rounded-md border border-solid border-slate-200 p-3">
            <h4 className="text-sm font-semibold">
                Version history (current: v{headVersion})
            </h4>
            {error ? (
                <p className="mt-2 text-sm text-red-600">{error}</p>
            ) : null}
            {versions.length === 0 ? (
                <p className="mt-2 text-sm text-slate-500">No versions yet.</p>
            ) : (
                <ul className="mt-2 space-y-1">
                    {versions.map((item) => (
                        <li
                            key={item.version}
                            className="flex items-center justify-between text-sm"
                        >
                            <span>
                                v{item.version} —{' '}
                                {new Date(item.created_at).toLocaleString()}
                            </span>
                            {item.version !== headVersion ? (
                                <Button
                                    size="sm"
                                    variant="outline"
                                    onClick={() => restore(item.version)}
                                    disabled={busy}
                                >
                                    Restore
                                </Button>
                            ) : (
                                <span className="text-xs text-slate-500">
                                    current
                                </span>
                            )}
                        </li>
                    ))}
                </ul>
            )}
        </div>
    );
};
