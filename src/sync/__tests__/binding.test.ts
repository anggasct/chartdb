import { describe, expect, it } from 'vitest';
import * as Y from 'yjs';
import { decodeDocToPayload, encodeDiagramToDoc } from '../binding';

describe('sync-binding', () => {
    it('upstream doc roundtrips through Y.Map layout', () => {
        const upstream = {
            id: 'd1',
            name: 'Shop',
            databaseType: 'GENERIC',
            tables: [
                { id: 't1', name: 'users' },
                { id: 't2', name: 'orders' },
            ],
            relationships: [],
            createdAt: new Date('2026-01-01T00:00:00.000Z').toISOString(),
            updatedAt: new Date('2026-01-02T00:00:00.000Z').toISOString(),
        };
        const doc = encodeDiagramToDoc(upstream);
        const back = decodeDocToPayload<typeof upstream>(doc);
        expect(back).toEqual(upstream);
    });

    it('two yjs docs with concurrent field edits converge', () => {
        const a = new Y.Doc();
        const b = new Y.Doc();
        a.getMap('m').set('a', 1);
        b.getMap('m').set('b', 2);
        const updA = Y.encodeStateAsUpdate(a);
        const updB = Y.encodeStateAsUpdate(b);
        Y.applyUpdate(b, updA);
        Y.applyUpdate(a, updB);
        expect(a.getMap('m').get('a')).toBe(1);
        expect(a.getMap('m').get('b')).toBe(2);
        expect(b.getMap('m').get('a')).toBe(1);
        expect(b.getMap('m').get('b')).toBe(2);
        const hashA = Buffer.from(Y.encodeStateAsUpdate(a)).toString('hex');
        const hashB = Buffer.from(Y.encodeStateAsUpdate(b)).toString('hex');
        expect(hashA).toBe(hashB);
    });
});
