package com.mobileegress.agent.ui

internal fun <Frame : AutoCloseable> analyzeQrFrame(
    frame: Frame,
    read: (Frame) -> String?,
    onDecoded: (String) -> Unit,
    onUnrecognized: () -> Unit,
    onUnavailable: () -> Unit,
) {
    try {
        val value = try {
            read(frame)
        } catch (_: Exception) {
            onUnavailable()
            return
        } catch (_: LinkageError) {
            onUnavailable()
            return
        }
        if (value != null) {
            if (value.isEmpty()) onUnrecognized() else onDecoded(value)
        }
    } finally {
        frame.close()
    }
}
