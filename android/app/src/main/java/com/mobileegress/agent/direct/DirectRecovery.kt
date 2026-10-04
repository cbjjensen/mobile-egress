package com.mobileegress.agent.direct

import android.security.keystore.KeyPermanentlyInvalidatedException
import com.mobileegress.agent.pairing.PairingBundleParser
import com.mobileegress.agent.security.CredentialStoreException
import com.mobileegress.agent.security.PinnedTls
import com.mobileegress.agent.status.ErrorClass
import java.security.PrivateKey
import java.security.cert.CertificateExpiredException
import java.security.cert.CertificateNotYetValidException
import java.security.cert.X509Certificate
import java.time.Instant
import java.util.Date

enum class DirectRecovery(val message: String, val automaticRetry: Boolean, val error: ErrorClass) {
    None("", true, ErrorClass.None),
    RePairRequired("Pairing is no longer usable. Remove this Client, then scan a fresh invitation from the workload.", false, ErrorClass.RelayAuth),
    UnlockStorage("Phone credential storage is unavailable. Unlock the phone or restore storage, then Retry.", false, ErrorClass.Credential),
    CheckClock("Certificate is not valid yet. Check the phone and workload date and time, then Retry.", false, ErrorClass.Credential),
    ClientStorageUnavailable("Client storage is unavailable. Restore storage on the workload; retrying automatically.", true, ErrorClass.RelayUnavailable),
    RetryConnection("Retrying: check cellular, Client address and device clocks. Import an updated address; if Client trust was replaced, remove and pair again.", true, ErrorClass.RelayUnavailable),
    RetryWhileStopped("Client unavailable. Check cellular and the Client, then Retry or start sharing for automatic recovery.", false, ErrorClass.RelayUnavailable),
}

fun directRecovery(error: Exception): DirectRecovery = when {
    error is DirectRetryWhileStoppedException -> DirectRecovery.RetryWhileStopped
    error is CredentialStoreException -> DirectRecovery.UnlockStorage
    error is DirectException -> when (error.code) {
        "client_repair_required", "client_certificate_expired", "client_trust_invalid", "key_unavailable", "identity_unavailable" -> DirectRecovery.RePairRequired
        "credential_storage_unavailable" -> DirectRecovery.UnlockStorage
        "client_clock_invalid" -> DirectRecovery.CheckClock
        "client_storage_unavailable" -> DirectRecovery.ClientStorageUnavailable
        else -> DirectRecovery.RetryConnection
    }
    else -> DirectRecovery.RetryConnection
}

internal fun directPrivateKey(alias: String, read: (String) -> PrivateKey?): PrivateKey = try {
    read(alias) ?: throw DirectException("key_unavailable")
} catch (error: DirectException) { throw error }
catch (_: KeyPermanentlyInvalidatedException) { throw DirectException("key_unavailable") }
catch (_: Exception) { throw DirectException("credential_storage_unavailable") }

private fun localCertificate(pem: String, now: Instant): X509Certificate {
    val certificate = try { PinnedTls.parseCertificateChain(pem).first() }
        catch (_: Exception) { throw DirectException("client_trust_invalid") }
    try { certificate.checkValidity(Date.from(now)) }
    catch (_: CertificateExpiredException) { throw DirectException("client_certificate_expired") }
    catch (_: CertificateNotYetValidException) { throw DirectException("client_clock_invalid") }
    return certificate
}

internal fun directCa(record: DirectRecord, now: Instant = Instant.now()): X509Certificate {
    localCertificate(record.caCertificatePem, now)
    return try { PairingBundleParser.parseCaCertificate(record.caCertificatePem, now) }
        catch (_: Exception) { throw DirectException("client_trust_invalid") }
}

internal fun directLeaf(record: DirectRecord, now: Instant = Instant.now()): X509Certificate =
    localCertificate(record.identity?.certificatePem ?: throw DirectException("identity_unavailable"), now)
