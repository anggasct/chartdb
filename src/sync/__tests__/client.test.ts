import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import * as Y from 'yjs';
import * as syncProtocol from 'y-protocols/sync';
import * as encoding from 'lib0/encoding';
import { DiagramSyncClient } from '../client';
import { encodeSyncEnvelope } from '../protocol';

class FakeWebSocket {
    static OPEN = 1;
    readyState = FakeWebSocket.OPEN;
    binaryType = '';
    sent: Uint8Array[] = [];
    onopen: (() => void) | null = null;
    onmessage: ((event: { data: Uint8Array }) => void) | null = null;
    onerror: (() => void) | null = null;
    onclose: (() => void) | null = null;
    send(data: Uint8Array): void {
        this.sent.push(data);
    }
    close(): void {}
}

describe('sync-client remote origin', () => {
    const RealWebSocket = globalThis.WebSocket;
    let sockets: FakeWebSocket[];

    beforeEach(() => {
        sockets = [];
        vi.stubGlobal(
            'WebSocket',
            class extends FakeWebSocket {
                constructor() {
                    super();
                    sockets.push(this);
                }
            } as unknown as typeof WebSocket
        );
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        expect(globalThis.WebSocket).toBe(RealWebSocket);
    });

    it('does not re-send updates applied from incoming sync messages', () => {
        const doc = new Y.Doc();
        const client = new DiagramSyncClient('d1', doc);
        client.connect();
        const ws = sockets[0];
        ws.onopen?.();
        ws.sent.length = 0;

        doc.getMap('m').set('local', 1);
        expect(ws.sent.length).toBe(1);

        const remote = new Y.Doc();
        remote.getMap('m').set('remote', 2);
        const update = Y.encodeStateAsUpdate(remote);
        const enc = encoding.createEncoder();
        syncProtocol.writeUpdate(enc, update);
        const msg = encodeSyncEnvelope(0, encoding.toUint8Array(enc));
        ws.onmessage?.({ data: msg });

        expect(ws.sent.length).toBe(1);
        expect(doc.getMap('m').get('remote')).toBe(2);
        expect(doc.getMap('m').get('local')).toBe(1);
        client.disconnect();
    });
});
