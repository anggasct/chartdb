import * as Y from 'yjs';
import * as syncProtocol from 'y-protocols/sync';
import * as awarenessProtocol from 'y-protocols/awareness';
import * as encoding from 'lib0/encoding';
import * as decoding from 'lib0/decoding';
import {
    ENVELOPE_AWARENESS,
    ENVELOPE_CONTROL,
    ENVELOPE_SYNC,
    decodeControl,
    decodeEnvelope,
    decodeSyncPayload,
    encodeAwarenessEnvelope,
    encodeControl,
    encodeSyncEnvelope,
} from './protocol';

export type SyncStatus =
    | 'connecting'
    | 'live'
    | 'reconnecting'
    | 'reset'
    | 'denied';

export interface PresenceUser {
    id: string;
    name: string;
    color: string;
}

export interface PeerPresence {
    clientId: number;
    user: PresenceUser;
    selected: string[];
}

export interface SyncClientOptions {
    user?: PresenceUser;
    onStatus?: (status: SyncStatus) => void;
    onPeers?: (peers: PeerPresence[]) => void;
}

export class DiagramSyncClient {
    private ws: WebSocket | null = null;
    private epoch = 0;
    private status: SyncStatus = 'connecting';
    private reconnectAttempts = 0;
    private closed = false;
    private awareness: awarenessProtocol.Awareness;
    private opts: SyncClientOptions;
    private readonly remoteOrigin = Symbol('remote-sync');

    constructor(
        private diagramId: string,
        private doc: Y.Doc,
        opts: SyncClientOptions = {}
    ) {
        this.opts = opts;
        this.awareness = new awarenessProtocol.Awareness(doc);
        if (opts.user) {
            this.awareness.setLocalState({
                user: opts.user,
                selected: [],
            });
        }
        this.awareness.on('change', () => {
            const peers: PeerPresence[] = [];
            this.awareness.getStates().forEach((state, clientId) => {
                if (clientId === this.doc.clientID) {
                    return;
                }
                const user = (state as { user?: PresenceUser }).user;
                if (!user) {
                    return;
                }
                const selected =
                    (state as { selected?: string[] }).selected ?? [];
                peers.push({ clientId, user, selected });
            });
            opts.onPeers?.(peers);
        });
        this.doc.on('update', (update: Uint8Array, origin: unknown) => {
            if (origin === this.remoteOrigin) {
                return;
            }
            if (this.ws?.readyState === WebSocket.OPEN) {
                const enc = encoding.createEncoder();
                syncProtocol.writeUpdate(enc, update);
                const inner = encoding.toUint8Array(enc);
                this.ws.send(encodeSyncEnvelope(this.epoch, inner));
            }
        });
        this.awareness.on('update', ({ added, updated, removed }: never) => {
            void added;
            void updated;
            void removed;
            if (this.ws?.readyState === WebSocket.OPEN) {
                const clients = Array.from(
                    this.awareness.getStates().keys()
                ).filter((id) => id === this.doc.clientID);
                void clients;
                const upd = awarenessProtocol.encodeAwarenessUpdate(
                    this.awareness,
                    [this.doc.clientID]
                );
                this.ws.send(encodeAwarenessEnvelope(upd));
            }
        });
    }

    connect(): void {
        this.closed = false;
        this.setStatus('connecting');
        const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const url = `${proto}//${window.location.host}/api/v1/diagram/${this.diagramId}/sync`;
        const ws = new WebSocket(url);
        this.ws = ws;
        ws.binaryType = 'arraybuffer';
        ws.onopen = () => {
            this.reconnectAttempts = 0;
            const enc = encoding.createEncoder();
            syncProtocol.writeSyncStep1(enc, this.doc);
            ws.send(encodeSyncEnvelope(this.epoch, encoding.toUint8Array(enc)));
        };
        ws.onmessage = (event) => {
            const msg =
                event.data instanceof ArrayBuffer
                    ? new Uint8Array(event.data)
                    : new Uint8Array(event.data);
            const { envelope, payload } = decodeEnvelope(msg);
            if (envelope === ENVELOPE_SYNC) {
                const { epoch, inner } = decodeSyncPayload(payload);
                if (epoch !== this.epoch && this.epoch !== 0) {
                    return;
                }
                if (this.epoch === 0) {
                    this.epoch = epoch;
                }
                const enc = encoding.createEncoder();
                const dec = decoding.createDecoder(inner);
                syncProtocol.readSyncMessage(
                    dec,
                    enc,
                    this.doc,
                    this.remoteOrigin
                );
                if (encoding.length(enc) > 1) {
                    ws.send(
                        encodeSyncEnvelope(
                            this.epoch,
                            encoding.toUint8Array(enc)
                        )
                    );
                }
                this.setStatus('live');
            } else if (envelope === ENVELOPE_AWARENESS) {
                awarenessProtocol.applyAwarenessUpdate(
                    this.awareness,
                    payload,
                    null
                );
            } else if (envelope === ENVELOPE_CONTROL) {
                const ctrl = decodeControl(payload);
                if (ctrl.kind === 'epoch' && typeof ctrl.epoch === 'number') {
                    this.epoch = ctrl.epoch;
                } else if (ctrl.kind === 'reset') {
                    if (typeof ctrl.epoch === 'number') {
                        this.epoch = ctrl.epoch;
                    }
                    this.setStatus('reset');
                    const enc = encoding.createEncoder();
                    syncProtocol.writeSyncStep1(enc, this.doc);
                    ws.send(
                        encodeSyncEnvelope(
                            this.epoch,
                            encoding.toUint8Array(enc)
                        )
                    );
                    this.setStatus('live');
                }
            }
        };
        ws.onerror = () => {
            if (!this.closed) {
                this.scheduleReconnect();
            }
        };
        ws.onclose = () => {
            if (!this.closed) {
                this.scheduleReconnect();
            }
        };
    }

    setSelection(selected: string[]): void {
        const state = this.awareness.getLocalState() as {
            user?: PresenceUser;
        } | null;
        this.awareness.setLocalState({
            user: state?.user ??
                this.opts.user ?? { id: '', name: '', color: '' },
            selected,
        });
    }

    disconnect(): void {
        this.closed = true;
        try {
            const upd = awarenessProtocol.encodeAwarenessUpdate(
                this.awareness,
                [this.doc.clientID]
            );
            void upd;
            awarenessProtocol.removeAwarenessStates(
                this.awareness,
                [this.doc.clientID],
                null
            );
        } catch {
            // ignore
        }
        this.ws?.close();
        this.ws = null;
    }

    private setStatus(status: SyncStatus): void {
        this.status = status;
        this.opts.onStatus?.(status);
    }

    private scheduleReconnect(): void {
        this.setStatus('reconnecting');
        this.ws = null;
        const delay = Math.min(1000 * 2 ** this.reconnectAttempts, 10000);
        this.reconnectAttempts += 1;
        window.setTimeout(() => {
            if (!this.closed) {
                this.connect();
            }
        }, delay);
    }

    getStatus(): SyncStatus {
        return this.status;
    }
}

export function createSyncDocument(): Y.Doc {
    return new Y.Doc();
}

export { encodeControl };
