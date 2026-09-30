import com.intellij.openapi.util.Disposer
import org.jetbrains.kotlin.allopen.AllOpenComponentRegistrar
import org.jetbrains.kotlin.allopen.AllOpenConfigurationKeys
import org.jetbrains.kotlin.analysis.api.KaExperimentalApi
import org.jetbrains.kotlin.analysis.api.analyze
import org.jetbrains.kotlin.analysis.api.components.KaDiagnosticCheckerFilter
import org.jetbrains.kotlin.analysis.api.diagnostics.KaSeverity
import org.jetbrains.kotlin.analysis.api.projectStructure.KaSourceModule
import org.jetbrains.kotlin.analysis.api.standalone.StandaloneAnalysisAPISession
import org.jetbrains.kotlin.analysis.api.standalone.buildStandaloneAnalysisAPISession
import org.jetbrains.kotlin.analysis.project.structure.builder.buildKtLibraryModule
import org.jetbrains.kotlin.analysis.project.structure.builder.buildKtSdkModule
import org.jetbrains.kotlin.analysis.project.structure.builder.buildKtSourceModule
import org.jetbrains.kotlin.cli.extensionsStorage
import org.jetbrains.kotlin.compiler.plugin.CompilerPluginRegistrar
import org.jetbrains.kotlin.config.CompilerConfiguration
import org.jetbrains.kotlin.platform.jvm.JvmPlatforms
import org.jetbrains.kotlin.psi.KtFile
import org.jetbrains.kotlin.psi.KtPsiFactory
import org.jetbrains.kotlin.analysis.api.projectStructure.contextModule
import java.io.File
import java.nio.file.Path
import kotlin.io.path.Path
import kotlin.system.exitProcess
import kotlin.system.measureTimeMillis

class Model(path: String) {
    private val rows = File(path).readLines().map { it.split("\t") }
    fun paths(kind: String, set: String): List<Path> =
        rows.firstOrNull { it[0] == kind && it[2] == set }?.getOrNull(3).orEmpty()
            .split(":").filter { it.isNotBlank() }.distinct().map { Path(it) }
    // "pluginId:key=value" options of a source set's Kotlin compilation.
    fun pluginOptions(set: String): List<Pair<String, String>> =
        rows.filter { it[0] == "KTPLS-PLUGIN" && it[2] == set }.map { r ->
            val (id, kv) = r[3].split(":", limit = 2)
            id to kv
        }
}

// The compiler plugins the analyzer knows, configured from the model.
fun pluginConfiguration(model: Model): CompilerConfiguration {
    val config = CompilerConfiguration()
    config.extensionsStorage = CompilerPluginRegistrar.ExtensionStorage()
    val opts = (model.pluginOptions("main") + model.pluginOptions("test")).distinct()
    val allOpen = opts.filter { it.first == "org.jetbrains.kotlin.allopen" }.map { it.second }
    if (allOpen.isNotEmpty()) {
        config.add(CompilerPluginRegistrar.COMPILER_PLUGIN_REGISTRARS, AllOpenComponentRegistrar())
        config.put(AllOpenConfigurationKeys.ALLOPEN_ANNOTATION, allOpen.filter { it.startsWith("annotation=") }.map { it.removePrefix("annotation=") })
        config.put(AllOpenConfigurationKeys.ALLOPEN_PRESET, allOpen.filter { it.startsWith("preset=") }.map { it.removePrefix("preset=") })
    }
    return config
}

fun buildSession(model: Model, jdkHome: String, disposable: com.intellij.openapi.Disposable): StandaloneAnalysisAPISession {
    val mainSources = model.paths("KTPLS-SOURCES", "main")
    val testSources = model.paths("KTPLS-SOURCES", "test")
    // Only jars: the build's class directories would shadow the sources.
    val mainCp = model.paths("KTPLS-CLASSPATH", "main").filter { it.toString().endsWith(".jar") }
    val testCp = model.paths("KTPLS-CLASSPATH", "test").filter { it.toString().endsWith(".jar") }
    return buildStandaloneAnalysisAPISession(disposable) {
        // Public in bytecode but not visible to Kotlin callers.
        javaClass.getMethod("registerCompilerPluginServices", CompilerConfiguration::class.java)
            .invoke(this, pluginConfiguration(model))
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

data class Diag(val severity: KaSeverity, val factory: String, val message: String)

@OptIn(KaExperimentalApi::class)
fun diagnose(file: KtFile): List<Diag> = analyze(file) {
    file.collectDiagnostics(KaDiagnosticCheckerFilter.ONLY_COMMON_CHECKERS)
        .map { Diag(it.severity, it.factoryName, it.defaultMessage) }
}

fun usedMb(): Long {
    val rt = Runtime.getRuntime()
    System.gc()
    return (rt.totalMemory() - rt.freeMemory()) / (1024 * 1024)
}

// A session over the project as it was on disk when it was built.
class Workspace(model: Model, jdkHome: String) {
    private val disposable = Disposer.newDisposable("ktpls-session")
    val session = buildSession(model, jdkHome, disposable)
    private val moduleOf = session.modulesWithFiles.flatMap { (m, fs) -> fs.map { it to m } }.toMap()
    val files: List<KtFile> = moduleOf.keys.filterIsInstance<KtFile>()

    fun file(pathSuffix: String): KtFile = files.first { it.virtualFile.path.endsWith(pathSuffix) }

    // Diagnostics for new text of a file, at once: the text is analyzed as
    // an in-memory copy in the file's module, against the rest of the
    // project as of this session.
    fun check(file: KtFile, text: String): List<Diag> {
        val copy = KtPsiFactory(session.project).createFile(file.name, text)
        copy.contextModule = moduleOf.getValue(file)
        return diagnose(copy)
    }

    fun close() = Disposer.dispose(disposable)
}

fun errorsOf(ds: List<Diag>) = ds.filter { it.severity == KaSeverity.ERROR }

// args: <model.txt> <jdkHome> <file A suffix> <file B suffix>
fun main(args: Array<String>) {
    try {
        run(args)
    } catch (t: Throwable) {
        t.printStackTrace()
        exitProcess(1)
    }
    exitProcess(0)
}

fun run(args: Array<String>) {
    val model = Model(args[0])
    lateinit var ws: Workspace
    val tSession = measureTimeMillis { ws = Workspace(model, args[1]) }
    println("session built in ${tSession}ms: ${ws.files.size} Kotlin files, heap ${usedMb()}MB")

    // Every file, as on startup: code that compiles should have no errors.
    val errorsByFactory = mutableMapOf<String, Int>()
    var warnings = 0
    val tAll = measureTimeMillis {
        for (f in ws.files) for (d in diagnose(f)) {
            if (d.severity == KaSeverity.ERROR) errorsByFactory.merge(d.factory, 1, Int::plus)
            else if (d.severity == KaSeverity.WARNING) warnings++
        }
    }
    println("all files in ${tAll}ms: ${errorsByFactory.values.sum()} errors, $warnings warnings; heap ${usedMb()}MB")
    errorsByFactory.forEach { (k, v) -> println("  error $k: $v") }

    val a = ws.file(args[2])
    val b = ws.file(args[3])
    val aPath = File(a.virtualFile.path)
    val bPath = File(b.virtualFile.path)
    val aText = aPath.readText()
    val bText = bPath.readText()
    try {
        saveTests(ws, a, b, aPath, bPath, aText, bText, args)
    } finally {
        aPath.writeText(aText) // restore the copies, whatever happened
        bPath.writeText(bText)
    }
}

fun saveTests(ws: Workspace, a: KtFile, b: KtFile, aPath: File, bPath: File, aText: String, bText: String, args: Array<String>) {

    // Instant check of a saved file's new text in the current session.
    fun check(file: File, text: String, psi: KtFile, label: String) {
        file.writeText(text)
        lateinit var ds: List<Diag>
        val t = measureTimeMillis { ds = ws.check(psi, text) }
        println("instant: %-40s %5dms  errors=%s".format(label, t, errorsOf(ds).map { it.factory }))
    }

    // 1. An error introduced and fixed in one file.
    check(aPath, aText + "\nval ktplsProbe: Int = \"not an int\"\n", a, "A with a type error")
    check(aPath, aText, a, "A fixed")

    // 2. Across files: B calls a function A doesn't declare yet...
    check(bPath, bText + "\nfun ktplsUse() = ktplsNewFunction()\n", b, "B calls a missing function")
    // ...then A is saved declaring it. The instant check sees A alone; B
    // catches up when the session is rebuilt in the background.
    check(aPath, aText + "\nfun ktplsNewFunction() = 1\n", a, "A declares it")

    lateinit var fresh: Workspace
    val tRebuild = measureTimeMillis {
        ws.close()
        fresh = Workspace(Model(args[0]), args[1])
    }
    lateinit var bErrors: List<Diag>
    val tB = measureTimeMillis { bErrors = errorsOf(diagnose(fresh.file(args[3]))) }
    println("rebuild: session %dms, then B analyzed in %dms: errors=%s".format(tRebuild, tB, bErrors.map { it.factory }))
    lateinit var aErrors: List<Diag>
    val tA = measureTimeMillis { aErrors = errorsOf(diagnose(fresh.file(args[2]))) }
    println("         A analyzed in %dms: errors=%s; heap %dMB".format(tA, aErrors.map { it.factory }, usedMb()))
    fresh.close()
}
