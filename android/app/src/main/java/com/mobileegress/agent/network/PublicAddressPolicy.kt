package com.mobileegress.agent.network

import java.net.Inet6Address
import java.net.InetAddress

class DestinationRejected : IllegalArgumentException("Destination is not a public TCP address")

object PublicAddressPolicy {
    private val publicIpv6Prefix = prefix("2000::", 3)

    private val forbiddenPrefixes = listOf(
        prefix("0.0.0.0", 8),
        prefix("10.0.0.0", 8),
        prefix("100.64.0.0", 10),
        prefix("127.0.0.0", 8),
        prefix("169.254.0.0", 16),
        prefix("172.16.0.0", 12),
        prefix("192.0.0.0", 24),
        prefix("192.0.2.0", 24),
        prefix("192.88.99.0", 24),
        prefix("192.168.0.0", 16),
        prefix("198.18.0.0", 15),
        prefix("198.51.100.0", 24),
        prefix("203.0.113.0", 24),
        prefix("224.0.0.0", 4),
        prefix("240.0.0.0", 4),
        prefix("::", 128),
        prefix("::1", 128),
        prefix("100::", 64),
        prefix("2001::", 23),
        prefix("2001:2::", 48),
        prefix("2001:db8::", 32),
        prefix("2002::", 16),
        prefix("3fff::", 20),
        prefix("fc00::", 7),
        prefix("fe80::", 10),
        prefix("ff00::", 8),
    )

    fun validate(ipLiteral: String, port: Int): InetAddress {
        if (port !in 1..65_535) throw DestinationRejected()
        val address = parseLiteral(ipLiteral) ?: throw DestinationRejected()
        if (
            address.isAnyLocalAddress ||
            address.isLoopbackAddress ||
            address.isLinkLocalAddress ||
            address.isSiteLocalAddress ||
            address.isMulticastAddress ||
            (address is Inet6Address && !publicIpv6Prefix.contains(address.address)) ||
            forbiddenPrefixes.any { it.contains(address.address) }
        ) {
            throw DestinationRejected()
        }
        return address
    }

    private fun parseLiteral(value: String): InetAddress? {
        if (value.isEmpty() || value != value.trim() || '%' in value) return null
        if (':' in value) {
            if (value.any { it !in "0123456789abcdefABCDEF:." }) return null
            return try {
                InetAddress.getByName(value).takeIf { it is Inet6Address }
            } catch (_: Exception) {
                null
            }
        }
        val octets = value.split('.')
        if (octets.size != 4) return null
        val bytes = ByteArray(4)
        octets.forEachIndexed { index, octet ->
            if (octet.isEmpty() || octet.length > 3 || octet.any { !it.isDigit() }) return null
            if (octet.length > 1 && octet[0] == '0') return null
            val number = octet.toIntOrNull()?.takeIf { it in 0..255 } ?: return null
            bytes[index] = number.toByte()
        }
        return InetAddress.getByAddress(bytes)
    }

    private fun prefix(address: String, bits: Int): IpPrefix =
        IpPrefix(requireNotNull(parseLiteral(address)).address, bits)

    private data class IpPrefix(val network: ByteArray, val bits: Int) {
        fun contains(candidate: ByteArray): Boolean {
            if (candidate.size != network.size) return false
            val fullBytes = bits / 8
            for (index in 0 until fullBytes) {
                if (candidate[index] != network[index]) return false
            }
            val remainingBits = bits % 8
            if (remainingBits == 0) return true
            val mask = (0xFF shl (8 - remainingBits)) and 0xFF
            return (candidate[fullBytes].toInt() and mask) == (network[fullBytes].toInt() and mask)
        }
    }
}
