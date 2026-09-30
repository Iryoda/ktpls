# ktpls analyzer (prototype)

A sibling JVM process for ktpls that runs the Kotlin compiler's front end
(the Kotlin Analysis API, K2) over the project, for compiler diagnostics
without a Gradle build. **Prototype**: `Spike.kt` builds a standalone
analysis session from a project model and reports diagnostics for every
file, with timings.

- `gradle/ktpls-model.gradle`: a Gradle init script printing each
  project's source folders and compile classpath (the project model).
- Kotlin 2.4.20 Analysis API (`*-for-ide` artifacts from JetBrains'
  intellij-dependencies repository) on IntelliJ platform 251.27812.49,
  bundled into one jar (`gradle shadowJar` → `build/libs/analyzer.jar`):
  the Analysis API's descriptors must be loadable together, and the
  platform jars must come before the older copies inside kotlin-compiler.

Run the prototype:

```sh
./gradlew -I analyzer/gradle/ktpls-model.gradle ktplsModel -q --no-configuration-cache > model.txt   # in the project
java -Xmx4g -cp build/libs/analyzer.jar SpikeKt model.txt "$JAVA_HOME"
```
