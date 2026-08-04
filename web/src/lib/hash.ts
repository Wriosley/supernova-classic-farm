const SHA256_LENGTH = 32

export async function sha256(bytes: Uint8Array): Promise<Uint8Array> {
  const digest = await crypto.subtle.digest('SHA-256', bytes)
  return new Uint8Array(digest)
}

export function bytesEqual(left: Uint8Array, right: Uint8Array): boolean {
  if (left.byteLength !== right.byteLength) {
    return false
  }

  let difference = 0
  for (let index = 0; index < left.byteLength; index += 1) {
    difference |= left[index] ^ right[index]
  }
  return difference === 0
}

export async function verifySha256(
  bytes: Uint8Array,
  expectedDigest: Uint8Array,
): Promise<void> {
  if (expectedDigest.byteLength !== SHA256_LENGTH) {
    throw new Error(`配置摘要长度无效：${expectedDigest.byteLength}`)
  }

  const actualDigest = await sha256(bytes)
  if (!bytesEqual(actualDigest, expectedDigest)) {
    throw new Error('客户端配置 SHA-256 校验失败')
  }
}
