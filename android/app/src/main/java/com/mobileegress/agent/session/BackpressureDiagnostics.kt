package com.mobileegress.agent.session

import android.util.Log

/**
 * Emits only bounded-queue causes. It deliberately carries no identities, destinations, stream
 * IDs, or payload data so production log capture can be enabled safely while diagnosing pressure.
 */
internal enum class BackpressureSource {
    TargetPerStreamLimit,
    TargetSessionFrameLimit,
    TargetSessionByteLimit,
    TargetCommandQueue,
    TargetOutboundMailbox,
    RequiredControlSaturation,
}

internal fun interface BackpressureReporter {
    fun report(source: BackpressureSource)
}

internal object NoOpBackpressureReporter : BackpressureReporter {
    override fun report(source: BackpressureSource) = Unit
}

internal object LogcatBackpressureReporter : BackpressureReporter {
    override fun report(source: BackpressureSource) {
        Log.w(LOG_TAG, "backpressure source=${source.name}")
    }

    private const val LOG_TAG = "AgentBackpressure"
}
