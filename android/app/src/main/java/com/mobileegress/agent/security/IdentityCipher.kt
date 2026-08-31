package com.mobileegress.agent.security

import javax.crypto.Cipher
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

internal data class EncryptedIdentityPayload(
    val iv: ByteArray,
    val ciphertext: ByteArray,
)

internal fun encryptIdentityPayload(
    cleartext: ByteArray,
    key: SecretKey,
): EncryptedIdentityPayload {
    val cipher = Cipher.getInstance(IDENTITY_TRANSFORMATION)
    cipher.init(Cipher.ENCRYPT_MODE, key)
    cipher.updateAAD(IDENTITY_ASSOCIATED_DATA)
    val ciphertext = cipher.doFinal(cleartext)
    val iv = requireNotNull(cipher.iv).copyOf()
    require(iv.isNotEmpty())
    return EncryptedIdentityPayload(iv, ciphertext)
}

internal fun decryptIdentityPayload(
    payload: EncryptedIdentityPayload,
    key: SecretKey,
): ByteArray {
    val cipher = Cipher.getInstance(IDENTITY_TRANSFORMATION)
    cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, payload.iv))
    cipher.updateAAD(IDENTITY_ASSOCIATED_DATA)
    return cipher.doFinal(payload.ciphertext)
}

private const val IDENTITY_TRANSFORMATION = "AES/GCM/NoPadding"
private val IDENTITY_ASSOCIATED_DATA = "mobile-egress-agent-identity-v1".encodeToByteArray()
