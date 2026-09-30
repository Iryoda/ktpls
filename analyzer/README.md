# ktpls analyzer

A sibling JVM process for ktpls, like jdtls is for Java editors: it runs
the Kotlin compiler's front end (the Kotlin Analysis API, K2) over the
project, for IntelliJ-like diagnostics as you type, and for hovers and
definitions of library declarations. ktpls starts it, and stops it on
exit.

- `Session.kt` builds a standalone analysis session from the project
  model (source folders, classpaths and compiler plugin options, read
  from Gradle by `internal/analyzer/ktpls-model.gradle`), and checks a
  buffer's text as an in-memory copy of its file.
- `Hover.kt` resolves a reference: its declaration, the call's types, and
  the declaration's docs from the library's sources jar.
- `Main.kt` is the protocol: one JSON request or response per line on
  stdin and stdout (init, rebuild, check, diagnose, hover, shutdown);
  interactive requests run first.

It uses Kotlin 2.4.20's Analysis API (`*-for-ide` artifacts from
JetBrains' intellij-dependencies repository) on IntelliJ platform
251.27812.49, bundled into one jar; the platform jars come before the
older copies inside kotlin-compiler.

Build it (the Mason install does this):

```sh
./gradlew shadowJar   # → build/libs/analyzer.jar
```
