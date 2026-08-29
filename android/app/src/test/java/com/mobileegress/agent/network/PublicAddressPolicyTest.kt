package com.mobileegress.agent.network

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class PublicAddressPolicyTest {
    @Test
    fun `accepts only public IP literals and valid TCP ports`() {
        assertEquals("1.1.1.1", PublicAddressPolicy.validate("1.1.1.1", 443).hostAddress)
        assertEquals(
            "2606:4700:4700:0:0:0:0:1111",
            PublicAddressPolicy.validate("2606:4700:4700::1111", 443).hostAddress,
        )
    }

    @Test
    fun `rejects private reserved and non-literal destinations`() {
        listOf(
            "0.0.0.0",
            "10.0.0.1",
            "100.64.0.1",
            "127.0.0.1",
            "169.254.1.1",
            "172.16.0.1",
            "192.0.0.1",
            "192.0.2.1",
            "192.88.99.1",
            "192.168.1.1",
            "198.18.0.1",
            "198.51.100.1",
            "203.0.113.1",
            "224.0.0.1",
            "240.0.0.1",
            "::",
            "::1",
            "100::1",
            "2001:2::1",
            "2001:db8::1",
            "fc00::1",
            "fe80::1",
            "ff00::1",
            "example.com",
            "1.1.1.1.example.com",
        ).forEach { address ->
            assertThrows(DestinationRejected::class.java) {
                PublicAddressPolicy.validate(address, 443)
            }
        }
    }

    @Test
    fun `rejects invalid ports`() {
        listOf(0, 65_536).forEach { port ->
            assertThrows(DestinationRejected::class.java) {
                PublicAddressPolicy.validate("1.1.1.1", port)
            }
        }
    }
}
