plugins {
    kotlin("jvm") version "2.4.20"
    application
    id("com.gradleup.shadow") version "9.0.2"
}

val kotlinVersion = "2.4.20"
val intellij = "251.27812.49"

dependencies {
    // The IntelliJ platform the Analysis API was compiled against: first, so
    // its classes win over the older copies bundled in kotlin-compiler.
    listOf(
        "platform:core", "platform:core-impl", "platform:util", "platform:util-base", "platform:util-rt",
        "platform:util-class-loader", "platform:util-text-matching", "platform:util-xml-dom", "platform:extensions",
        "platform:util-jdom", "platform:util-coroutines", "platform:diagnostic", "platform:core-ui",
        "java:java-psi", "java:java-psi-impl",
    ).forEach { implementation("com.jetbrains.intellij.$it:$intellij") }
    implementation("org.jetbrains.kotlin:kotlin-compiler:$kotlinVersion") { isTransitive = false }
    listOf(
        "analysis-api-for-ide", "analysis-api-standalone-for-ide", "analysis-api-impl-base-for-ide",
        "analysis-api-k2-for-ide", "low-level-api-fir-for-ide", "analysis-api-platform-interface-for-ide",
        "symbol-light-classes-for-ide", "kotlin-compiler-common-for-ide",
    ).forEach { implementation("org.jetbrains.kotlin:$it:$kotlinVersion") { isTransitive = false } }
    implementation("org.jetbrains.kotlin:kotlin-allopen-compiler-plugin:$kotlinVersion") { isTransitive = false }
    implementation("com.github.ben-manes.caffeine:caffeine:2.9.3")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core-jvm:1.10.2")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-core-jvm:1.8.1")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json-jvm:1.8.1")
}

kotlin {
    jvmToolchain(21)
    compilerOptions {
        optIn.addAll(
            "org.jetbrains.kotlin.analysis.api.KaExperimentalApi",
            "org.jetbrains.kotlin.analysis.api.KaIdeApi",
            "org.jetbrains.kotlin.analysis.api.KaPlatformInterface",
            "org.jetbrains.kotlin.psi.KtExperimentalApi",
            "org.jetbrains.kotlin.compiler.plugin.ExperimentalCompilerApi",
            "org.jetbrains.kotlin.config.CompilerConfiguration.Internals",
        )
    }
}
application { mainClass.set("MainKt") }


tasks.shadowJar {
    mergeServiceFiles()
    isZip64 = true
    archiveFileName.set("analyzer.jar")
    manifest { attributes["Main-Class"] = "MainKt" }
}
