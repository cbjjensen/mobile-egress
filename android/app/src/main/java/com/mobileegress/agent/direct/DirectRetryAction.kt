package com.mobileegress.agent.direct

import kotlinx.coroutines.CancellationException

internal class DirectRetryWhileStoppedException(cause: Exception) : Exception(cause)

/** The Client row's Retry action, shared with the foreground service recovery path. */
internal suspend fun retryDirectClient(
    id: String,
    registry: DirectRegistry,
    retryWhileStopped: suspend (String) -> DirectRecord,
): DirectRecord {
    registry.removals.requireAllowed(id)
    val record = registry.get(id)
    if (!record.enabled) return record
    // The live service owns maintenance and its backoff loop. A stopped service must
    // decline the handoff so the row action can perform a single explicit attempt.
    if (DirectRetrySignals.retry(id)) return record
    registry.removals.requireAllowed(id)
    registry.get(id).let { if (!it.enabled) return it }
    return try { retryWhileStopped(id) }
    catch (cancelled: CancellationException) { throw cancelled }
    catch (error: Exception) {
        if (directRecovery(error).automaticRetry &&
            (error !is DirectException || error.code !in listOf("removal_pending", "removal_stop_not_saved", "client_removed"))) {
            throw DirectRetryWhileStoppedException(error)
        }
        throw error
    }
}
