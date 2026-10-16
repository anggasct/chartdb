import * as encoding from 'lib0/encoding';
import * as decoding from 'lib0/decoding';

export const ENVELOPE_SYNC = 0;
export const ENVELOPE_AWARENESS = 1;
export const ENVELOPE_CONTROL = 2;

export interface ControlMessage {
    kind: 'epoch' | 'reset';
    epoch?: number;
}

export function encodeSyncEnvelope(
    epoch: number,
    inner: Uint8Array
): Uint8Array {
    const enc = encoding.createEncoder();
    encoding.writeVarUint(enc, ENVELOPE_SYNC);
    encoding.writeVarUint(enc, epoch);
    encoding.writeUint8Array(enc, inner);
    return encoding.toUint8Array(enc);
}

export function decodeSyncPayload(payload: Uint8Array): {
    epoch: number;
    inner: Uint8Array;
} {
    const dec = decoding.createDecoder(payload);
    const epoch = decoding.readVarUint(dec);
    const inner = decoding.readTailAsUint8Array(dec);
    return { epoch, inner: inner.slice() };
}

export function encodeAwarenessEnvelope(payload: Uint8Array): Uint8Array {
    const enc = encoding.createEncoder();
    encoding.writeVarUint(enc, ENVELOPE_AWARENESS);
    encoding.writeUint8Array(enc, payload);
    return encoding.toUint8Array(enc);
}

export function encodeControl(
    kind: 'epoch' | 'reset',
    epoch: number
): Uint8Array {
    const enc = encoding.createEncoder();
    encoding.writeVarUint(enc, ENVELOPE_CONTROL);
    const json = JSON.stringify({ kind, epoch });
    encoding.writeUint8Array(enc, new TextEncoder().encode(json));
    return encoding.toUint8Array(enc);
}

export function decodeEnvelope(msg: Uint8Array): {
    envelope: number;
    payload: Uint8Array;
} {
    const dec = decoding.createDecoder(msg);
    const envelope = decoding.readVarUint(dec);
    const payload = decoding.readTailAsUint8Array(dec);
    return { envelope, payload: payload.slice() };
}

export function decodeControl(payload: Uint8Array): ControlMessage {
    const text = new TextDecoder().decode(payload);
    return JSON.parse(text) as ControlMessage;
}
