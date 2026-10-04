package com.mobileegress.agent.direct

import java.util.concurrent.CopyOnWriteArrayList

/** Nonsecret removal intent must remain readable when the credential keystore is unavailable. */
class DirectRemovalGate(
    initial: Set<String> = emptySet(),
    private val persist: (Set<String>) -> Unit = {},
) {
    private val mutationLock = Any()
    @Volatile private var blocked = initial.toSet()
    private val listeners = CopyOnWriteArrayList<(String) -> Unit>()

    fun isBlocked(id: String) = id in blocked
    fun requireAllowed(id: String) { if (isBlocked(id)) throw DirectException("removal_pending") }
    fun <T> serialized(action: () -> T): T = synchronized(mutationLock) { action() }

    fun block(id: String) = serialized {
        // Stop live traffic before either the plaintext latch or encrypted registry touches disk.
        blocked = blocked + id
        listeners.forEach { it(id) }
        DirectRegistrySignals.changed()
        try { persist(blocked) } catch (_: Exception) { throw DirectException("removal_stop_not_saved") }
    }

    fun clear(id: String) = serialized {
        if (id in blocked) {
            val next = blocked - id
            // Failure to save an explicit recovery must leave this process blocked as well.
            try { persist(next) } catch (_: Exception) { throw DirectException("removal_pending") }
            blocked = next
            DirectRegistrySignals.changed()
        }
    }

    fun observeBlocked(listener: (String) -> Unit): () -> Unit {
        listeners.add(listener)
        return { listeners.remove(listener) }
    }
}
