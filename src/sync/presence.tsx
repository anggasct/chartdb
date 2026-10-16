import React from 'react';
import type { PeerPresence, SyncStatus } from './client';

export const SyncBadge: React.FC<{ status: SyncStatus; peers: number }> = ({
    status,
    peers,
}) => {
    const label =
        status === 'live'
            ? `live${peers > 0 ? ` · ${peers}` : ''}`
            : status === 'connecting'
              ? 'connecting…'
              : status === 'reconnecting'
                ? 'reconnecting…'
                : status === 'reset'
                  ? 'reloaded'
                  : 'no access';
    return (
        <span
            data-testid="sync-badge"
            className="inline-flex items-center rounded bg-slate-100 px-2 py-1 text-xs text-slate-600"
        >
            <span
                data-testid="sync-dot"
                className={
                    status === 'live'
                        ? 'mr-1 inline-block size-2 rounded-full bg-green-500'
                        : 'mr-1 inline-block size-2 rounded-full bg-slate-400'
                }
            />
            {label}
        </span>
    );
};

export const PresenceAvatars: React.FC<{ peers: PeerPresence[] }> = ({
    peers,
}) => {
    if (peers.length === 0) {
        return null;
    }
    return (
        <div data-testid="presence-avatars" className="flex -space-x-1">
            {peers.map((p) => (
                <span
                    key={p.clientId}
                    title={p.user.name}
                    data-testid={`presence-${p.clientId}`}
                    className="inline-flex size-6 items-center justify-center rounded-full text-[10px] font-semibold text-white"
                    style={{ backgroundColor: p.user.color || '#64748b' }}
                >
                    {(p.user.name || '?').slice(0, 1).toUpperCase()}
                </span>
            ))}
        </div>
    );
};
