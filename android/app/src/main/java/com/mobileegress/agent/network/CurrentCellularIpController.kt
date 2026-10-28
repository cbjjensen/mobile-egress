package com.mobileegress.agent.network

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

data class CurrentCellularIpState(
    val snapshot: PublicIpSnapshot = PublicIpSnapshot(),
    val checking: Boolean = false,
    val canRefresh: Boolean = false,
)

/** Foreground dashboard checks only. All entry points run on the main thread. */
class CurrentCellularIpController<N : Any>(
    private val scope: CoroutineScope,
    private val probe: suspend (N) -> PublicIpSnapshot,
    private val changed: (CurrentCellularIpState) -> Unit,
) {
    var state = CurrentCellularIpState()
        private set
    private var network: N? = null
    private var rotating = false
    private var generation = 0L
    private var job: Job? = null

    fun networkChanged(network: N?) {
        this.network = network
        invalidate()
        refresh()
    }

    fun networkLost(network: N) {
        if (this.network == network) networkChanged(null)
    }

    fun setRotating(rotating: Boolean) {
        if (this.rotating == rotating) return
        this.rotating = rotating
        invalidate()
        refresh()
    }

    fun refresh() {
        val selected = network ?: return
        if (rotating || state.checking) return
        val expected = ++generation
        publish(CurrentCellularIpState(checking = true, canRefresh = true))
        job = scope.launch {
            val snapshot = try { probe(selected) }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { PublicIpSnapshot() }
            if (generation == expected) {
                job = null
                publish(CurrentCellularIpState(snapshot = snapshot, canRefresh = true))
            }
        }
    }

    private fun invalidate() {
        generation++
        job?.cancel(); job = null
        publish(CurrentCellularIpState(canRefresh = network != null && !rotating))
    }

    private fun publish(value: CurrentCellularIpState) { state = value; changed(value) }
}
