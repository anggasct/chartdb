import { describe, expect, it } from 'vitest';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as Y from 'yjs';

function fixtureBytes(name: string): Uint8Array {
    const p = path.resolve(process.cwd(), 'src/sync/__tests__/fixtures', name);
    return new Uint8Array(fs.readFileSync(p));
}

function fixtureText(name: string): string {
    const p = path.resolve(process.cwd(), 'src/sync/__tests__/fixtures', name);
    return fs.readFileSync(p, 'utf8');
}

describe('fixtures', () => {
    it('ygo-produced update applies in yjs with expected content', () => {
        const version = fixtureText('ygo-update.version');
        expect(version).toMatch(/ygo@v1\.51\.11/);
        const expected = JSON.parse(fixtureText('ygo-update.expected.json'));
        const update = fixtureBytes('ygo-update.bin');
        const doc = new Y.Doc();
        Y.applyUpdate(doc, update);
        const content = doc.getMap('diagram').get('content');
        expect(typeof content).toBe('string');
        expect(JSON.parse(content as string)).toEqual(expected);
    });
});
