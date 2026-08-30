package com.mobileegress.agent.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

private val OledMint = Color(0xFF7EF2C5)
private val OledBlue = Color(0xFF7DB7FF)
private val OledViolet = Color(0xFFD6B3FF)
private val OledWarning = Color(0xFFF4DF74)
private val OledError = Color(0xFFFF8D98)

private val OledColors = darkColorScheme(
    primary = OledMint,
    onPrimary = Color(0xFF03060C),
    primaryContainer = Color(0xFF17131C),
    onPrimaryContainer = OledViolet,
    secondary = OledWarning,
    onSecondary = Color(0xFF03060C),
    secondaryContainer = Color(0xFF1B190B),
    onSecondaryContainer = OledWarning,
    tertiary = OledMint,
    onTertiary = Color(0xFF03060C),
    tertiaryContainer = Color(0xFF0B1B16),
    onTertiaryContainer = Color(0xFFB6FFE3),
    background = Color.Black,
    onBackground = Color(0xFFF2F5FB),
    surface = Color(0xFF080A0F),
    onSurface = Color(0xFFF2F5FB),
    surfaceVariant = Color(0xFF0B0E14),
    onSurfaceVariant = Color(0xFFAEB7C6),
    surfaceContainerLowest = Color.Black,
    surfaceContainerLow = Color(0xFF05070B),
    surfaceContainer = Color(0xFF080A0F),
    surfaceContainerHigh = Color(0xFF0B0E14),
    surfaceContainerHighest = Color(0xFF0C111A),
    outline = Color(0xFF454B55),
    outlineVariant = Color(0xFF1C1D1F),
    error = OledError,
    onError = Color(0xFF03060C),
    errorContainer = Color(0xFF281014),
    onErrorContainer = Color(0xFFFFADB6),
    scrim = Color.Black,
)

internal fun selectAgentColorScheme(
    @Suppress("UNUSED_PARAMETER") systemDarkTheme: Boolean,
): ColorScheme = OledColors

internal fun oledToneColor(tone: ScreenTone): Color = when (tone) {
    ScreenTone.Neutral -> Color(0xFFAEB7C6)
    ScreenTone.Accent -> OledViolet
    ScreenTone.Info -> OledBlue
    ScreenTone.Success -> OledMint
    ScreenTone.Warning -> OledWarning
    ScreenTone.Error -> OledError
}

private val AppTypography = Typography(
    headlineLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Bold,
        fontSize = 34.sp,
        lineHeight = 39.sp,
        letterSpacing = (-0.6).sp,
    ),
    headlineSmall = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.SemiBold,
        fontSize = 24.sp,
        lineHeight = 30.sp,
    ),
    titleLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.SemiBold,
        fontSize = 20.sp,
        lineHeight = 26.sp,
    ),
    bodyLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Normal,
        fontSize = 16.sp,
        lineHeight = 24.sp,
    ),
    bodyMedium = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.Normal,
        fontSize = 14.sp,
        lineHeight = 21.sp,
    ),
    labelLarge = TextStyle(
        fontFamily = FontFamily.SansSerif,
        fontWeight = FontWeight.SemiBold,
        fontSize = 14.sp,
        lineHeight = 20.sp,
    ),
)

@Composable
fun MobileEgressTheme(content: @Composable () -> Unit) {
    val colorScheme = selectAgentColorScheme(systemDarkTheme = isSystemInDarkTheme())
    MaterialTheme(
        colorScheme = colorScheme,
        typography = AppTypography,
        content = content,
    )
}
