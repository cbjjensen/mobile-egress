package com.mobileegress.agent.ui

import android.app.Activity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.core.view.WindowCompat

private val DarkColors = darkColorScheme(
    primary = Color(0xFFC9ABFF),
    onPrimary = Color(0xFF32105E),
    primaryContainer = Color(0xFF452A70),
    onPrimaryContainer = Color(0xFFEBDCFF),
    secondary = Color(0xFFFFC56B),
    onSecondary = Color(0xFF452B00),
    tertiary = Color(0xFF62DEBD),
    onTertiary = Color(0xFF00382D),
    background = Color(0xFF0C0810),
    onBackground = Color(0xFFF3ECF6),
    surface = Color(0xFF18111E),
    onSurface = Color(0xFFF3ECF6),
    surfaceVariant = Color(0xFF251C2C),
    onSurfaceVariant = Color(0xFFCEC2D3),
    outline = Color(0xFF56485F),
    error = Color(0xFFFFB3BA),
    onError = Color(0xFF68001A),
)

private val LightColors = lightColorScheme(
    primary = Color(0xFF6A3EAE),
    onPrimary = Color.White,
    primaryContainer = Color(0xFFEBDCFF),
    onPrimaryContainer = Color(0xFF260057),
    secondary = Color(0xFF805600),
    onSecondary = Color.White,
    tertiary = Color(0xFF006B55),
    onTertiary = Color.White,
    background = Color(0xFFFBF8FD),
    onBackground = Color(0xFF1D1920),
    surface = Color(0xFFFFFBFF),
    onSurface = Color(0xFF1D1920),
    surfaceVariant = Color(0xFFF0E8F2),
    onSurfaceVariant = Color(0xFF4D4451),
    outline = Color(0xFF7E7283),
    error = Color(0xFFBA1A1A),
    onError = Color.White,
)

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
    val darkTheme = isSystemInDarkTheme()
    val view = LocalView.current
    if (!view.isInEditMode) {
        SideEffect {
            val window = (view.context as Activity).window
            WindowCompat.getInsetsController(window, view).apply {
                isAppearanceLightStatusBars = !darkTheme
                isAppearanceLightNavigationBars = !darkTheme
            }
        }
    }
    MaterialTheme(
        colorScheme = if (darkTheme) DarkColors else LightColors,
        typography = AppTypography,
        content = content,
    )
}
