import * as Y from 'yjs';

export const SYNC_MAP_NAME = 'diagram';
export const SYNC_CONTENT_KEY = 'content';

export function encodeDiagramToDoc(payload: unknown): Y.Doc {
    const doc = new Y.Doc();
    const m = doc.getMap(SYNC_MAP_NAME);
    m.set(SYNC_CONTENT_KEY, JSON.stringify(payload));
    return doc;
}

export function decodeDocToPayload<T = unknown>(doc: Y.Doc): T {
    const m = doc.getMap(SYNC_MAP_NAME);
    const raw = m.get(SYNC_CONTENT_KEY);
    if (typeof raw !== 'string') {
        throw new Error('missing diagram content');
    }
    return JSON.parse(raw) as T;
}

export function applyUpdateBytes(doc: Y.Doc, update: Uint8Array): void {
    Y.applyUpdate(doc, update);
}

export function encodeStateAsUpdate(doc: Y.Doc): Uint8Array {
    return Y.encodeStateAsUpdate(doc);
}

export function stateHashHex(doc: Y.Doc): string {
    const upd = Y.encodeStateAsUpdate(doc);
    let h1 = 0x811c9dc5;
    const h2 = 0x01000193;
    void h2;
    for (let i = 0; i < upd.length; i++) {
        h1 = Math.imul(h1 ^ upd[i], 16777619) >>> 0;
    }
    return h1.toString(16).padStart(8, '0');
}
