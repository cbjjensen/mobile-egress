package com.mobileegress.agent.network

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import org.junit.Assert.*
import org.junit.Test

@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class CurrentCellularIpControllerTest {
    @Test fun `same network address change invalidates pending probe without duplicate initial check`() = runTest {
        val old = CompletableDeferred<PublicIpSnapshot>()
        var calls = 0
        val controller = CurrentCellularIpController<String>(this, {
            if (++calls == 1) withContext(NonCancellable) { old.await() }
            else PublicIpSnapshot(ipv6 = "2001:db8::2")
        }) {}
        controller.networkChanged("cell"); runCurrent()
        controller.networkAddressesChanged("cell", listOf("2001:db8::1/64")); runCurrent()
        assertEquals(1, calls)
        controller.networkAddressesChanged("cell", listOf("2001:db8::2/64")); runCurrent()
        assertEquals("2001:db8::2", controller.state.snapshot.ipv6)
        old.complete(PublicIpSnapshot(ipv6 = "2001:db8::1")); runCurrent()
        assertEquals("2001:db8::2", controller.state.snapshot.ipv6)
        controller.networkAddressesChanged("old", listOf("2001:db8::3/64")); runCurrent()
        assertEquals(2, calls)
    }

    @Test fun `available cellular checks before sharing and refresh coalesces`() = runTest {
        var calls = 0
        val result = CompletableDeferred<PublicIpSnapshot>()
        val controller = CurrentCellularIpController<String>(this, { calls++; result.await() }) {}
        controller.networkChanged("cell-1")
        runCurrent()
        assertTrue(controller.state.checking)
        controller.refresh(); runCurrent()
        assertEquals(1, calls)
        result.complete(PublicIpSnapshot(ipv4 = "198.51.100.10", ipv6 = "2001:db8::1"))
        runCurrent()
        assertEquals("198.51.100.10", controller.state.snapshot.ipv4)
        assertEquals("2001:db8::1", controller.state.snapshot.ipv6)
        assertFalse(controller.state.checking)
        assertTrue(controller.state.canRefresh)
    }

    @Test fun `network replacement and loss reject even uncancellable old results`() = runTest {
        val old = CompletableDeferred<PublicIpSnapshot>()
        val controller = CurrentCellularIpController<String>(this, { token ->
            if (token == "old") withContext(NonCancellable) { old.await() }
            else PublicIpSnapshot(ipv4 = "198.51.100.20")
        }) {}
        controller.networkChanged("old"); runCurrent()
        controller.networkChanged("new"); runCurrent()
        assertEquals("198.51.100.20", controller.state.snapshot.ipv4)
        controller.networkLost("old")
        assertEquals("198.51.100.20", controller.state.snapshot.ipv4)
        old.complete(PublicIpSnapshot(ipv4 = "198.51.100.10")); runCurrent()
        assertEquals("198.51.100.20", controller.state.snapshot.ipv4)
        controller.networkLost("new")
        assertEquals(PublicIpSnapshot(), controller.state.snapshot)
        assertFalse(controller.state.canRefresh)
    }

    @Test fun `rotation clears pending address and resumes only on current network`() = runTest {
        val pending = CompletableDeferred<PublicIpSnapshot>()
        var calls = 0
        val controller = CurrentCellularIpController<String>(this, {
            calls++
            if (calls == 1) withContext(NonCancellable) { pending.await() }
            else PublicIpSnapshot(ipv6 = "2001:db8::2")
        }) {}
        controller.networkChanged("cell"); runCurrent()
        controller.setRotating(true)
        pending.complete(PublicIpSnapshot(ipv4 = "198.51.100.10")); runCurrent()
        assertEquals(PublicIpSnapshot(), controller.state.snapshot)
        controller.refresh(); runCurrent(); assertEquals(1, calls)
        controller.setRotating(false); runCurrent()
        assertEquals(PublicIpSnapshot(ipv6 = "2001:db8::2"), controller.state.snapshot)
        controller.networkChanged(null)
        assertFalse(controller.state.canRefresh)
        assertEquals(PublicIpSnapshot(), controller.state.snapshot)
    }

    @Test fun `refresh failure clears previously verified address`() = runTest {
        var calls = 0
        val controller = CurrentCellularIpController<String>(this, {
            if (++calls == 1) PublicIpSnapshot(ipv4 = "198.51.100.10") else error("probe unavailable")
        }) {}
        controller.networkChanged("cell"); runCurrent()
        assertNotNull(controller.state.snapshot.ipv4)
        controller.refresh()
        assertEquals(PublicIpSnapshot(), controller.state.snapshot)
        runCurrent()
        assertFalse(controller.state.checking)
        assertEquals(PublicIpSnapshot(), controller.state.snapshot)
    }
}
