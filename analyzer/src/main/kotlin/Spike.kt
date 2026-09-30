import com.intellij.openapi.util.Disposer
import org.jetbrains.kotlin.analysis.api.KaExperimentalApi
import org.jetbrains.kotlin.analysis.api.analyze
import org.jetbrains.kotlin.analysis.api.components.KaDiagnosticCheckerFilter
import org.jetbrains.kotlin.analysis.api.diagnostics.KaSeverity
import org.jetbrains.kotlin.analysis.api.standalone.StandaloneAnalysisAPISession
import org.jetbrains.kotlin.analysis.api.standalone.buildStandaloneAnalysisAPISession
import org.jetbrains.kotlin.analysis.project.structure.builder.buildKtLibraryModule
import org.jetbrains.kotlin.analysis.project.structure.builder.buildKtSdkModule
import org.jetbrains.kotlin.analysis.project.structure.builder.buildKtSourceModule
import org.jetbrains.kotlin.platform.jvm.JvmPlatforms
import org.jetbrains.kotlin.psi.KtFile
import java.io.File
import java.nio.file.Path
import kotlin.io.path.Path
import kotlin.system.measureTimeMillis

class Model(path: String) {
    private val rows = File(path).readLines().map { it.split("\t") }
    fun paths(kind: String, set: String): List<Path> =
        rows.firstOrNull { it[0] == kind && it[2] == set }?.getOrNull(3).orEmpty()
            .split(":").filter { it.isNotBlank() }.map { Path(it) }
}

fun buildSession(model: Model, jdkHome: String): StandaloneAnalysisAPISession {
    val mainSources = model.paths("KTPLS-SOURCES", "main")
    val testSources = model.paths("KTPLS-SOURCES", "test")
    // Only jars: the build's class directories would shadow the sources.
    val mainCp = model.paths("KTPLS-CLASSPATH", "main").filter { it.toString().endsWith(".jar") }
    val testCp = model.paths("KTPLS-CLASSPATH", "test").filter { it.toString().endsWith(".jar") }
    return buildStandaloneAnalysisAPISession(Disposer.newDisposable("ktpls")) {
        buildKtModuleProvider {
            val jvm = JvmPlatforms.defaultJvmPlatform
            platform = jvm
            val jdk = addModule(buildKtSdkModule {
                addBinaryRootsFromJdkHome(Path(jdkHome), isJre = false)
                platform = jvm
                libraryName = "JDK"
            })
            val mainLibs = addModule(buildKtLibraryModule {
                addBinaryRoots(mainCp)
                platform = jvm
                libraryName = "main-deps"
            })
            val testLibs = addModule(buildKtLibraryModule {
                addBinaryRoots(testCp - mainCp.toSet())
                platform = jvm
                libraryName = "test-deps"
            })
            val main = addModule(buildKtSourceModule {
                addSourceRoots(mainSources)
                platform = jvm
                moduleName = "main"
                addRegularDependency(mainLibs)
                addRegularDependency(jdk)
            })
            addModule(buildKtSourceModule {
                addSourceRoots(testSources)
                platform = jvm
                moduleName = "test"
                addRegularDependency(main)
                addFriendDependency(main)
                addRegularDependency(mainLibs)
                addRegularDependency(testLibs)
                addRegularDependency(jdk)
            })
        }
    }
}

data class Counts(var errors: Int = 0, var warnings: Int = 0)

@OptIn(KaExperimentalApi::class)
fun diagnose(file: KtFile, counts: Counts, byFactory: MutableMap<String, Int>) {
    analyze(file) {
        for (d in file.collectDiagnostics(KaDiagnosticCheckerFilter.ONLY_COMMON_CHECKERS)) {
            if (d.severity == KaSeverity.ERROR) {
                counts.errors++
                byFactory.merge(d.factoryName, 1, Int::plus)
            } else if (d.severity == KaSeverity.WARNING) {
                counts.warnings++
            }
        }
    }
}

fun usedMb(): Long {
    val rt = Runtime.getRuntime()
    System.gc()
    return (rt.totalMemory() - rt.freeMemory()) / (1024 * 1024)
}

// args: <model.txt> <jdkHome>
fun main(args: Array<String>) {
    val model = Model(args[0])
    lateinit var session: StandaloneAnalysisAPISession
    val tSession = measureTimeMillis { session = buildSession(model, args[1]) }
    val files = session.modulesWithFiles.values.flatten().filterIsInstance<KtFile>()
    println("session built in ${tSession}ms: ${files.size} Kotlin files, heap ${usedMb()}MB")

    // Every file, as on startup: code that compiles should have no errors.
    val counts = Counts()
    val byFactory = mutableMapOf<String, Int>()
    val times = mutableListOf<Long>()
    val tAll = measureTimeMillis {
        for (f in files) times += measureTimeMillis { diagnose(f, counts, byFactory) }
    }
    times.sort()
    println("all files in ${tAll}ms: ${counts.errors} errors, ${counts.warnings} warnings; " +
        "per file p50=${times[times.size / 2]}ms p99=${times[times.size * 99 / 100]}ms max=${times.last()}ms; heap ${usedMb()}MB")
    byFactory.entries.sortedByDescending { it.value }.take(10).forEach { println("  error ${it.key}: ${it.value}") }

    // Re-analysis of files already analyzed once (warm caches).
    val again = measureTimeMillis { for (f in files.take(50)) diagnose(f, Counts(), mutableMapOf()) }
    println("re-analyzing 50 files: ${again}ms")
}
