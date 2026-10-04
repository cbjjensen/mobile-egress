package com.mobileegress.agent.security

import android.system.Os
import android.system.OsConstants
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.nio.file.Files
import java.nio.file.StandardCopyOption

/** Injects filesystem failure points, not the registry transaction or its encryption. */
internal interface CredentialFileOperations {
    fun read(): ByteArray?
    fun writeTemporary(bytes: ByteArray)
    fun replace()
    fun syncCurrent()
}

/** Callers serialize access across all store instances. Never exposes a merely visible write. */
internal class DurableCredentialFile(private val files: CredentialFileOperations) {
    constructor(file: File) : this(AndroidCredentialFileOperations(file))
    private var knownDurable = false
    private var generation = 0L
    private var committed: ByteArray? = null
    val committedGeneration: Long? @Synchronized get() = generation.takeIf { knownDurable }

    @Synchronized fun read(): ByteArray? {
        if (knownDurable) return committed?.copyOf()
        val value = files.read()
        // A previous rename may have succeeded while its durability barrier failed. Before
        // exposing either that value or absence, confirm the current file and directory entry.
        files.syncCurrent()
        committed = value?.copyOf()
        knownDurable = true
        generation++
        return value
    }

    @Synchronized fun write(value: ByteArray) {
        knownDurable = false
        files.writeTemporary(value)
        files.replace()
        files.syncCurrent()
        committed = value.copyOf()
        knownDurable = true
        generation++
    }
}

private class AndroidCredentialFileOperations(private val file: File) : CredentialFileOperations {
    private val temporary = File(file.parentFile, file.name + ".new")
    override fun read(): ByteArray? {
        if (!file.exists()) return null
        require(file.length() <= MAX_BYTES) { "Credential file is too large" }
        return FileInputStream(file).use { input ->
            val output = java.io.ByteArrayOutputStream()
            val buffer = ByteArray(16 * 1024)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                require(output.size() + count <= MAX_BYTES) { "Credential file is too large" }
                output.write(buffer, 0, count)
            }
            output.toByteArray()
        }
    }
    override fun writeTemporary(bytes: ByteArray) {
        require(bytes.size <= MAX_BYTES)
        FileOutputStream(temporary).use { it.write(bytes); it.fd.sync() }
    }
    override fun replace() {
        // There is deliberately no non-atomic fallback.
        Files.move(temporary.toPath(), file.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
    }
    override fun syncCurrent() {
        if (file.exists()) FileOutputStream(file, true).use { it.fd.sync() }
        val parent = requireNotNull(file.parentFile)
        require(parent.isDirectory)
        val directory = Os.open(parent.absolutePath, OsConstants.O_RDONLY, 0)
        try { Os.fsync(directory) } finally { Os.close(directory) }
    }
    companion object { private const val MAX_BYTES = 8 * 1024 * 1024 }
}
