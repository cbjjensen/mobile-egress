package com.mobileegress.agent.ui

import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertEquals
import org.junit.Test

class AgentThemeTest {
    @Test
    fun `system light and dark modes resolve to the same oled palette`() {
        val lightSystemPalette = selectAgentColorScheme(systemDarkTheme = false)
        val darkSystemPalette = selectAgentColorScheme(systemDarkTheme = true)

        assertEquals(darkSystemPalette, lightSystemPalette)
        assertEquals(Color.Black, lightSystemPalette.background)
        assertEquals(Color(0xFF080A0F), lightSystemPalette.surface)
        assertEquals(Color(0xFF7EF2C5), lightSystemPalette.primary)
    }

    @Test
    fun `oled semantic tones preserve the inevitable proxies accent roles`() {
        assertEquals(Color(0xFFD6B3FF), oledToneColor(ScreenTone.Accent))
        assertEquals(Color(0xFF7DB7FF), oledToneColor(ScreenTone.Info))
        assertEquals(Color(0xFF7EF2C5), oledToneColor(ScreenTone.Success))
        assertEquals(Color(0xFFF4DF74), oledToneColor(ScreenTone.Warning))
        assertEquals(Color(0xFFFF8D98), oledToneColor(ScreenTone.Error))
    }
}
