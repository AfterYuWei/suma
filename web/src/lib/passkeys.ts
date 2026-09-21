type WebAuthnOptions = { publicKey: Record<string, unknown> }

const decodeBase64URL = (value: string): ArrayBuffer => {
  const normalized = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = normalized + '='.repeat((4 - normalized.length % 4) % 4)
  const binary = atob(padded)
  return Uint8Array.from(binary, (character) => character.charCodeAt(0)).buffer
}

const encodeBase64URL = (value: ArrayBuffer): string => {
  const bytes = new Uint8Array(value)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
}

const descriptors = (value: unknown): PublicKeyCredentialDescriptor[] | undefined => {
  if (!Array.isArray(value)) return undefined
  return value.map((item) => {
    const row = item as { id: string; type?: PublicKeyCredentialType; transports?: AuthenticatorTransport[] }
    return { ...row, id: decodeBase64URL(row.id), type: row.type ?? 'public-key' }
  })
}

export const passkeysAvailable = () => window.isSecureContext && typeof window.PublicKeyCredential !== 'undefined' && !!navigator.credentials

export async function createPasskey(value: unknown): Promise<Record<string, unknown>> {
  if (!passkeysAvailable()) throw new Error('Passkeys require a supported browser and a secure connection.')
  const source = (value as WebAuthnOptions).publicKey
  const user = source.user as { id: string; name: string; displayName: string }
  const publicKey = {
    ...source,
    challenge: decodeBase64URL(source.challenge as string),
    user: { ...user, id: decodeBase64URL(user.id) },
    excludeCredentials: descriptors(source.excludeCredentials),
  } as PublicKeyCredentialCreationOptions
  const credential = await navigator.credentials.create({ publicKey })
  if (!(credential instanceof PublicKeyCredential) || !(credential.response instanceof AuthenticatorAttestationResponse)) throw new Error('Passkey registration was cancelled.')
  const response = credential.response
  return {
    id: credential.id,
    rawId: encodeBase64URL(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: encodeBase64URL(response.clientDataJSON),
      attestationObject: encodeBase64URL(response.attestationObject),
      transports: response.getTransports?.() ?? [],
    },
  }
}

export async function getPasskey(value: unknown): Promise<Record<string, unknown>> {
  if (!passkeysAvailable()) throw new Error('Passkeys require a supported browser and a secure connection.')
  const source = (value as WebAuthnOptions).publicKey
  const publicKey = {
    ...source,
    challenge: decodeBase64URL(source.challenge as string),
    allowCredentials: descriptors(source.allowCredentials),
  } as PublicKeyCredentialRequestOptions
  const credential = await navigator.credentials.get({ publicKey })
  if (!(credential instanceof PublicKeyCredential) || !(credential.response instanceof AuthenticatorAssertionResponse)) throw new Error('Passkey verification was cancelled.')
  const response = credential.response
  return {
    id: credential.id,
    rawId: encodeBase64URL(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: encodeBase64URL(response.clientDataJSON),
      authenticatorData: encodeBase64URL(response.authenticatorData),
      signature: encodeBase64URL(response.signature),
      userHandle: response.userHandle ? encodeBase64URL(response.userHandle) : null,
    },
  }
}
