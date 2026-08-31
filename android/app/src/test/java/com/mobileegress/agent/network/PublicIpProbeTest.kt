package com.mobileegress.agent.network

import java.net.InetAddress
import javax.net.SocketFactory
import kotlinx.coroutines.test.runTest
import okhttp3.Dns
import org.junit.Assert.assertSame
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PublicIpProbeTest {
    @Test
    fun `HTTP client retains the cellular socket factory and DNS binding`() {
        val socketFactory = SocketFactory.getDefault()
        val dns = object : Dns {
            override fun lookup(hostname: String): List<InetAddress> = emptyList()
        }

        val client = buildPublicIpHttpClient(
            CellularHttpBinding(socketFactory = socketFactory, dns = dns),
            requestTimeoutMillis = 8_000,
        )

        assertSame(socketFactory, client.socketFactory)
        assertSame(dns, client.dns)
    }

    @Test
    fun `collector trims and returns independently verified address families`() = runTest {
        val snapshot = collectPublicIps { family ->
            when (family) {
                IpFamily.Ipv4 -> " 198.51.100.20\n"
                IpFamily.Ipv6 -> "2001:db8::20"
            }
        }

        assertEquals("198.51.100.20", snapshot.ipv4)
        assertEquals("2001:db8::20", snapshot.ipv6)
    }

    @Test
    fun `IPv6 failure does not discard a valid IPv4 result`() = runTest {
        val snapshot = collectPublicIps { family ->
            when (family) {
                IpFamily.Ipv4 -> "198.51.100.20"
                IpFamily.Ipv6 -> throw IllegalStateException("IPv6 unavailable")
            }
        }

        assertEquals("198.51.100.20", snapshot.ipv4)
        assertNull(snapshot.ipv6)
    }

    @Test
    fun `responses from the wrong address family are rejected`() = runTest {
        val snapshot = collectPublicIps { family ->
            when (family) {
                IpFamily.Ipv4 -> "2001:db8::20"
                IpFamily.Ipv6 -> "198.51.100.20"
            }
        }

        assertNull(snapshot.ipv4)
        assertNull(snapshot.ipv6)
    }

    @Test
    fun `blank and malformed responses produce an unverified snapshot`() = runTest {
        val snapshot = collectPublicIps { family ->
            when (family) {
                IpFamily.Ipv4 -> ""
                IpFamily.Ipv6 -> "not-an-ip"
            }
        }

        assertEquals(PublicIpSnapshot(), snapshot)
    }
}
