package com.mobileegress.agent.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class QrFrameAnalysisTest {
    @Test fun `native initialization failure reports unavailable without leaking loader details`() {
        var unavailable = 0
        guardScannerInitialization({ unavailable++ }) { throw UnsatisfiedLinkError("private loader detail") }
        assertEquals(1, unavailable)
    }

    @Test fun `decoded frame is delivered once and closed once`() {
        val frame = Frame()
        val results = mutableListOf<String>()
        analyzeQrFrame(frame, read = { "public-fixture" }, onDecoded = results::add,
            onUnrecognized = { error("unexpected") }, onUnavailable = { error("unexpected") })
        assertEquals(listOf("public-fixture"), results)
        assertEquals(1, frame.closes)
    }

    @Test fun `no code keeps scanning and closes the frame`() {
        val frame = Frame()
        analyzeQrFrame(frame, read = { null }, onDecoded = { error("unexpected") },
            onUnrecognized = { error("unexpected") }, onUnavailable = { error("unexpected") })
        assertEquals(1, frame.closes)
    }

    @Test fun `empty recognized code is rejected and frame still closes`() {
        val frame = Frame()
        var unrecognized = 0
        analyzeQrFrame(frame, read = { "" }, onDecoded = { error("unexpected") },
            onUnrecognized = { unrecognized++ }, onUnavailable = { error("unexpected") })
        assertEquals(1, unrecognized)
        assertEquals(1, frame.closes)
    }

    @Test fun `decoder and missing native library failures both close frame and report unavailable`() {
        for (failure in listOf(IllegalStateException("private decoder detail"), UnsatisfiedLinkError("private loader detail"))) {
            val frame = Frame()
            var unavailable = 0
            analyzeQrFrame(frame, read = { throw failure }, onDecoded = { error("unexpected") },
                onUnrecognized = { error("unexpected") }, onUnavailable = { unavailable++ })
            assertEquals(1, unavailable)
            assertEquals(1, frame.closes)
        }
    }

    @Test fun `callback failures do not leak retained frames or become decoder failures`() {
        val frame = Frame()
        var unavailable = 0
        assertThrows(IllegalStateException::class.java) {
            analyzeQrFrame(frame, read = { "public-fixture" }, onDecoded = { error("callback failed") },
                onUnrecognized = { error("unexpected") }, onUnavailable = { unavailable++ })
        }
        assertEquals(0, unavailable)
        assertEquals(1, frame.closes)
    }

    private class Frame : AutoCloseable {
        var closes = 0
        override fun close() { closes++ }
    }
}
