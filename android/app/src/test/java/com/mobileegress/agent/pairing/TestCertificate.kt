package com.mobileegress.agent.pairing

import java.math.BigInteger
import java.security.KeyPairGenerator
import java.security.spec.ECGenParameterSpec
import java.time.Instant
import java.util.Base64
import java.util.Date
import javax.security.auth.x500.X500Principal
import org.bouncycastle.asn1.x509.BasicConstraints
import org.bouncycastle.asn1.x509.Extension
import org.bouncycastle.asn1.x509.KeyUsage
import org.bouncycastle.cert.jcajce.JcaX509CertificateConverter
import org.bouncycastle.cert.jcajce.JcaX509v3CertificateBuilder
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder

internal fun testCaPem(keyUsage: Int? = KeyUsage.keyCertSign or KeyUsage.digitalSignature): String {
    val keys = KeyPairGenerator.getInstance("EC").apply {
        initialize(ECGenParameterSpec("secp256r1"))
    }.generateKeyPair()
    val name = X500Principal("CN=mobile-egress-test-ca")
    val builder = JcaX509v3CertificateBuilder(
        name,
        BigInteger.ONE,
        Date.from(Instant.parse("2025-01-01T00:00:00Z")),
        Date.from(Instant.parse("2030-01-01T00:00:00Z")),
        name,
        keys.public,
    )
    builder.addExtension(Extension.basicConstraints, true, BasicConstraints(true))
    if (keyUsage != null) {
        builder.addExtension(Extension.keyUsage, true, KeyUsage(keyUsage))
    }
    val certificate = JcaX509CertificateConverter().getCertificate(
        builder.build(JcaContentSignerBuilder("SHA256withECDSA").build(keys.private)),
    )
    val body = Base64.getMimeEncoder(64, "\n".toByteArray()).encodeToString(certificate.encoded)
    return "-----BEGIN CERTIFICATE-----\n$body\n-----END CERTIFICATE-----\n"
}
