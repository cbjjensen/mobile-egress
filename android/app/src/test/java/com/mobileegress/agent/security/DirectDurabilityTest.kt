package com.mobileegress.agent.security

import android.content.SharedPreferences
import com.mobileegress.agent.direct.*
import com.mobileegress.agent.pairing.EnrollmentCredentialKeys
import java.lang.reflect.Proxy
import java.security.KeyPairGenerator
import java.util.Base64
import javax.crypto.KeyGenerator
import org.junit.Assert.*
import org.junit.Test

class DirectDurabilityTest {
    @Test fun committedSnapshotsAvoidSyncAndSeparateStoresObserveTheNextCommittedGeneration() {
        val preferences = DirtyPreferences()
        val files = FaultingFiles()
        val file = DurableCredentialFile(files)
        val key = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
        val first = SecureIdentityStore(preferences.api, DirectRemovalGate(), file) { key }
        val second = SecureIdentityStore(preferences.api, DirectRemovalGate(), file) { key }
        val saved = DirectRegistry(first).reserve(invitation()) { PendingKey("key", "csr") }
        assertEquals(saved, second.snapshot().records.single())
        val synced = files.syncCount
        repeat(20) { assertEquals(saved, first.snapshot().records.single()); assertEquals(saved, second.snapshot().records.single()) }
        assertEquals("Known durable snapshots must not fsync on traffic-status refresh", synced, files.syncCount)
        DirectRegistry(second).setEnabled("client", false)
        assertFalse(first.snapshot().records.single().enabled)
        assertFalse(second.snapshot().records.single().enabled)
        files.failCommit = true
        assertThrows(CredentialStoreException::class.java) { DirectRegistry(second).setEnabled("client", true) }
        assertThrows(CredentialStoreException::class.java) { first.snapshot() }
        files.failCommit = false
        assertTrue(first.snapshot().records.single().enabled)
        assertTrue(second.snapshot().records.single().enabled)
    }

    @Test fun failureBeforeTemporaryWriteOrRenameLeavesOnlyOldCommittedState() {
        for (beforeWrite in listOf(true, false)) {
            val preferences = DirtyPreferences()
            val files = FaultingFiles()
            val key = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
            fun registry() = DirectRegistry(SecureIdentityStore(preferences.api, DirectRemovalGate(), DurableCredentialFile(files)) { key })
            val registry = registry()
            val saved = registry.reserve(invitation()) { PendingKey("key", "csr") }
            files.failTemporary = beforeWrite
            files.failReplace = !beforeWrite
            assertThrows(CredentialStoreException::class.java) { registry.setEnabled("client", false) }
            assertEquals(saved, registry.get("client"))
            files.crashRestart()
            assertEquals(saved, registry().get("client"))
        }
    }

    @Test fun archivedLegacyIdentityIsDurablyRetiredBeforeMigrationCompletes() {
        for (restart in listOf(false, true)) {
            val preferences = DirtyPreferences()
            val files = FaultingFiles()
            val key = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
            val old = AgentIdentity("https://old.example", "agent", "AB", "old-key", "cert", "ca")
            val encrypted = encryptIdentityPayload(DirectBundles.json.encodeToString(old).encodeToByteArray(), key)
            val encoder = Base64.getUrlEncoder().withoutPadding()
            val value = "v1:" + encoder.encodeToString(encrypted.iv) + ":" + encoder.encodeToString(encrypted.ciphertext)
            preferences.visible["identity"] = value
            preferences.disk = preferences.visible.toMap()
            preferences.failWrites = true
            fun store() = SecureIdentityStore(preferences.api, DirectRemovalGate(), DurableCredentialFile(files)) { key }
            assertThrows(CredentialStoreException::class.java) { store().snapshot() }
            assertTrue(requireNotNull(files.disk).decodeToString().contains(value))
            assertEquals(value, preferences.disk["identity"])
            if (restart) { files.crashRestart(); preferences.visible = preferences.disk.toMutableMap() }
            preferences.failWrites = false
            assertTrue(store().snapshot().migrationRequired)
            assertNull(preferences.disk["identity"])
            assertNull(store().load())
            assertTrue(requireNotNull(files.disk).decodeToString().contains(value))
        }
    }

    @Test fun uncertainReservationKeepsItsKeyAndSuccessfulRetryUsesTheSameIdentity() {
        val preferences = DirtyPreferences()
        val files = FaultingFiles()
        val key = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
        fun registry() = DirectRegistry(SecureIdentityStore(preferences.api, DirectRemovalGate(), DurableCredentialFile(files)) { key })
        val first = registry()
        first.snapshot()
        val retired = mutableListOf<String>()
        var created = 0
        val keys = object : EnrollmentCredentialKeys {
            override fun create(): DeviceKey {
                created++
                val pair = KeyPairGenerator.getInstance("EC").apply { initialize(256) }.generateKeyPair()
                files.failCommit = true
                return DeviceKey("persisted-key", pair.private, pair.public)
            }
            override fun createCsrPem(deviceKey: DeviceKey) = "same-csr"
            override fun delete(alias: String) { retired.add(alias) }
        }
        assertThrows(CredentialStoreException::class.java) { reserveDirectIdentity(first, invitation(), keys) }
        assertTrue("An unknown write outcome must not retire a possibly referenced key", retired.isEmpty())
        files.failCommit = false
        val retry = reserveDirectIdentity(registry(), invitation(), keys)
        assertEquals("persisted-key", retry.keyAlias)
        assertEquals("same-csr", retry.csrPem)
        assertEquals(1, created)
        files.crashRestart()
        assertEquals(retry, registry().get("client"))
    }

    @Test fun interruptedIssuedWriteRecoversAfterRetryOrCrashWithoutPrematureAck() {
        for (restart in listOf(false, true)) {
            val preferences = DirtyPreferences()
            val files = FaultingFiles()
            val key = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
            fun registry() = DirectRegistry(SecureIdentityStore(preferences.api, DirectRemovalGate(), DurableCredentialFile(files)) { key })
            val first = registry()
            first.reserve(invitation()) { PendingKey("key", "same-csr") }
            var fail = true
            var ackCount = 0
            val csrRequests = mutableListOf<String>()
            val control = object : DirectControl {
                override fun enroll(record: DirectRecord): DirectIssued {
                    csrRequests.add(record.csrPem)
                    files.failCommit = fail
                    return DirectIssued("cert", "ca", "AB", "agent", record.clientId, "pair", 1)
                }
                override fun ack(record: DirectRecord) {
                    val committedCopy = FaultingFiles().apply { visible = files.disk?.copyOf(); disk = files.disk?.copyOf() }
                    val committed = SecureIdentityStore(preferences.api, DirectRemovalGate(), DurableCredentialFile(committedCopy)) { key }.snapshot()
                    assertEquals("AB", committed.records.single().identity!!.serial)
                    ackCount++
                }
                override fun config(record: DirectRecord) = ""
                override fun renew(record: DirectRecord): DirectIssued = error("unexpected renewal")
            }
            fun recover(registry: DirectRegistry) = DirectEnrollment(registry, verify = { record, issued ->
                AgentIdentity(record.endpoint, "agent", issued.serial, record.keyAlias, issued.certificatePem, issued.caCertificatePem)
            }).recover("client", control)
            assertThrows(CredentialStoreException::class.java) { recover(first) }
            assertEquals(0, ackCount)
            if (restart) files.crashRestart()
            fail = false; files.failCommit = false
            assertEquals(DirectStage.Paired, recover(registry()).stage)
            assertEquals(1, ackCount)
            assertTrue(csrRequests.all { it == "same-csr" })
            assertEquals(if (restart) 2 else 1, csrRequests.size)
        }
    }

    @Test fun migrationKeepsExistingCiphertextAndLegacyRecoveryWithoutActivePreferenceFallback() {
        val preferences = DirtyPreferences()
        val files = FaultingFiles()
        val key = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
        val previous = DirectRegistryState(records = listOf(DirectRecord("client", "private-display-name", "https://client.example", "key", "private-csr", "ca")))
        val encrypted = encryptIdentityPayload(DirectBundles.json.encodeToString(previous).encodeToByteArray(), key)
        val encoder = Base64.getUrlEncoder().withoutPadding()
        val ciphertext = "v2:" + encoder.encodeToString(encrypted.iv) + ":" + encoder.encodeToString(encrypted.ciphertext)
        preferences.visible["direct_registry"] = ciphertext
        preferences.visible["legacy_recovery"] = "v1:opaque:encrypted-recovery"
        val stops = DirectRemovalGate(setOf("client"))
        val store = SecureIdentityStore(preferences.api, stops, DurableCredentialFile(files)) { key }
        assertEquals(previous, store.snapshot())
        assertFalse(DirectRegistry(store).get("client").enabled)
        val archive = requireNotNull(files.disk).decodeToString()
        assertTrue(archive.contains(ciphertext))
        assertTrue(archive.contains("v1:opaque:encrypted-recovery"))
        assertFalse(archive.contains("private-display-name"))
        assertFalse(archive.contains("private-csr"))
        preferences.visible["direct_registry"] = "corrupt-old-preference"
        assertEquals(previous, SecureIdentityStore(preferences.api, stops, DurableCredentialFile(files)) { key }.snapshot())
        files.visible = "invalid-new-file".encodeToByteArray()
        // Only this process writes the private file. Corruption is detected on a fresh open.
        assertThrows(CredentialStoreException::class.java) {
            SecureIdentityStore(preferences.api, stops, DurableCredentialFile(files)) { key }.snapshot()
        }
    }

    @Test fun failedIssuedWriteNeverAcknowledgesMemoryOnlyIdentityOnRetry() {
        val preferences = DirtyPreferences()
        val files = FaultingFiles()
        val key = KeyGenerator.getInstance("AES").apply { init(256) }.generateKey()
        val registry = DirectRegistry(SecureIdentityStore(preferences.api, DirectRemovalGate(), DurableCredentialFile(files)) { key })
        val pending = registry.reserve(invitation()) { PendingKey("key", "csr") }
        var acknowledgements = 0
        val control = object : DirectControl {
            override fun enroll(record: DirectRecord): DirectIssued {
                preferences.failWrites = true
                files.failCommit = true
                return DirectIssued("cert", "ca", "AB", "agent", record.clientId, "pair", 1)
            }
            override fun ack(record: DirectRecord) { acknowledgements++ }
            override fun config(record: DirectRecord) = ""
            override fun renew(record: DirectRecord): DirectIssued = error("unexpected renewal")
        }
        val enrollment = DirectEnrollment(registry, verify = { record, issued ->
            AgentIdentity(record.endpoint, "agent", issued.serial, record.keyAlias, issued.certificatePem, issued.caCertificatePem)
        })
        assertThrows(CredentialStoreException::class.java) { enrollment.recover(pending.clientId, control) }
        assertThrows(CredentialStoreException::class.java) { enrollment.recover(pending.clientId, control) }
        assertEquals("An uncommitted issued identity must never authorize ACK", 0, acknowledgements)
    }

    private fun invitation() = DirectInvitation(2, "mobile-egress-direct-invitation", "client", "Client",
        "https://client.example", "ca", "invite", "cap", "2030-01-01T00:00:00Z", "agent")

    /** A rename is process-visible before the containing directory has been durably synced. */
    private class FaultingFiles : CredentialFileOperations {
        var failCommit = false
        var syncCount = 0
        var failTemporary = false
        var failReplace = false
        var visible: ByteArray? = null
        var disk: ByteArray? = null
        private var temporary: ByteArray? = null
        private var dirty = false
        override fun read() = visible?.copyOf()
        override fun writeTemporary(bytes: ByteArray) {
            if (failTemporary) throw java.io.IOException("simulated temporary write failure")
            temporary = bytes.copyOf()
        }
        override fun replace() {
            if (failReplace) throw java.io.IOException("simulated rename failure")
            visible = requireNotNull(temporary); dirty = true
        }
        override fun syncCurrent() {
            syncCount++
            if (dirty && failCommit) throw java.io.IOException("simulated directory sync failure")
            disk = visible?.copyOf(); dirty = false
        }
        fun crashRestart() { visible = disk?.copyOf(); temporary = null; dirty = false }
    }

    /** Models the actual SharedPreferences ordering: update process memory before disk commit. */
    private class DirtyPreferences {
        var failWrites = false
        var visible = mutableMapOf<String, String>()
        var disk = emptyMap<String, String>()
        val api = Proxy.newProxyInstance(SharedPreferences::class.java.classLoader, arrayOf(SharedPreferences::class.java)) { _, method, args ->
            when (method.name) {
                "getString" -> visible[args[0]] ?: args[1]
                "edit" -> editor()
                else -> error("Unexpected preferences operation ${method.name}")
            }
        } as SharedPreferences
        private fun editor(): SharedPreferences.Editor {
            val edits = mutableMapOf<String, String?>()
            lateinit var editor: SharedPreferences.Editor
            editor = Proxy.newProxyInstance(SharedPreferences.Editor::class.java.classLoader, arrayOf(SharedPreferences.Editor::class.java)) { _, method, args ->
                when (method.name) {
                    "putString" -> { edits[args[0] as String] = args[1] as String?; editor }
                    "remove" -> { edits[args[0] as String] = null; editor }
                    "commit" -> {
                        edits.forEach { (name, value) -> if (value == null) visible.remove(name) else visible[name] = value }
                        if (failWrites) false else { disk = visible.toMap(); true }
                    }
                    else -> error("Unexpected editor operation ${method.name}")
                }
            } as SharedPreferences.Editor
            return editor
        }
    }
}
