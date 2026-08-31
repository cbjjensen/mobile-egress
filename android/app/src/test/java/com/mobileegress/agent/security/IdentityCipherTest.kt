package com.mobileegress.agent.security

import javax.crypto.KeyGenerator
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Test

class IdentityCipherTest {
    @Test
    fun `encryption uses a provider-generated IV and round trips`() {
        val key = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
        val clear = "agent identity".encodeToByteArray()

        val first = encryptIdentityPayload(clear, key)
        val second = encryptIdentityPayload(clear, key)

        assertFalse(first.iv.isEmpty())
        assertNotEquals(first.iv.toList(), second.iv.toList())
        assertArrayEquals(clear, decryptIdentityPayload(first, key))
        assertArrayEquals(clear, decryptIdentityPayload(second, key))
    }
}
