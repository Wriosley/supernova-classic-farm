const HEX = Array.from({ length: 256 }, (_, value) =>
  value.toString(16).padStart(2, '0'),
)

// crypto.randomUUID is secure-context only, while crypto.getRandomValues is
// not; request IDs must stay real v4 UUIDs over plain HTTP too.
export function randomUuid(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') {
    return crypto.randomUUID()
  }

  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80

  const hex = Array.from(bytes, (byte) => HEX[byte])
  return [
    hex.slice(0, 4).join(''),
    hex.slice(4, 6).join(''),
    hex.slice(6, 8).join(''),
    hex.slice(8, 10).join(''),
    hex.slice(10, 16).join(''),
  ].join('-')
}
