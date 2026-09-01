package com.mobileegress.agent.session

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class StreamTombstonesTest {
    @Test
    fun `retains the most recent one thousand twenty four terminal stream ids`() {
        val tombstones = StreamTombstones()
        repeat(1_025) { index -> tombstones.remember("stream-$index") }

        assertFalse(tombstones.contains("stream-0"))
        assertTrue(tombstones.contains("stream-1"))
        assertTrue(tombstones.contains("stream-1024"))
    }

    @Test
    fun `remembering a duplicate does not evict a distinct tombstone`() {
        val tombstones = StreamTombstones(capacity = 2)
        tombstones.remember("first")
        tombstones.remember("second")
        tombstones.remember("first")

        assertTrue(tombstones.contains("first"))
        assertTrue(tombstones.contains("second"))
    }
}
