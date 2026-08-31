package com.mobileegress.agent.ui

class RotationSettingsLaunchGate {
    private var consumedAttemptId: Long? = null

    @Synchronized
    fun consume(attemptId: Long): Boolean {
        if (consumedAttemptId == attemptId) return false
        consumedAttemptId = attemptId
        return true
    }
}
