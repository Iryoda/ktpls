pluginManagement { repositories { gradlePluginPortal(); mavenCentral() } }
rootProject.name = "ktpls-analyzer-spike"
dependencyResolutionManagement {
    repositories {
        mavenCentral()
        maven("https://cache-redirector.jetbrains.com/intellij-dependencies")
        maven("https://cache-redirector.jetbrains.com/intellij-repository/releases")
    }
}
