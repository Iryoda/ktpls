import com.intellij.openapi.util.Disposer
import com.intellij.core.CoreApplicationEnvironment
import com.intellij.psi.PsiManager
import com.intellij.psi.PsiTreeChangeListener
import com.intellij.psi.AbstractFileViewProvider
import com.intellij.psi.impl.source.PsiFileImpl
import org.jetbrains.kotlin.allopen.AllOpenComponentRegistrar
import org.jetbrains.kotlin.allopen.AllOpenConfigurationKeys
import org.jetbrains.kotlin.analysis.api.KaExperimentalApi
import org.jetbrains.kotlin.analysis.api.analyze
import org.jetbrains.kotlin.analysis.api.components.KaDiagnosticCheckerFilter
import org.jetbrains.kotlin.analysis.api.diagnostics.KaSeverity
import org.jetbrains.kotlin.analysis.api.platform.modification.publishModuleOutOfBlockModificationEvent
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

fun buildSession(model: Model, jdkHome: String): StandaloneAnalysisAPISession {
    val mainSources = model.paths("KTPLS-SOURCES", "main")
    val testSources = model.paths("KTPLS-SOURCES", "test")
    // Only jars: the build's class directories would shadow the sources.
    val mainCp = model.paths("KTPLS-CLASSPATH", "main").filter { it.toString().endsWith(".jar") }
    val testCp = model.paths("KTPLS-CLASSPATH", "test").filter { it.toString().endsWith(".jar") }
    return buildStandaloneAnalysisAPISession(Disposer.newDisposable("ktpls")) {
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

class Workspace(val session: StandaloneAnalysisAPISession) {
    init {
        // Standalone mode doesn't register the extension point that
        // PsiManager.reloadFromDisk notifies.
        CoreApplicationEnvironment.registerExtensionPoint(
            session.project.extensionArea, "com.intellij.psi.treeChangeListener", PsiTreeChangeListener::class.java,
        )
    }
    private val moduleOf = session.modulesWithFiles.flatMap { (m, fs) -> fs.map { it to m } }.toMap()
    val files: List<KtFile> = moduleOf.keys.filterIsInstance<KtFile>()

    fun file(pathSuffix: String): KtFile = files.first { it.virtualFile.path.endsWith(pathSuffix) }

    // A file was saved: drop its cached text and syntax tree so they are
    // re-read from disk, and invalidate the analysis caches of its module
    // (the file's declarations may have changed).
    fun saved(file: KtFile): KtFile {
        (file.viewProvider as AbstractFileViewProvider).onContentReload()
        (file as PsiFileImpl).onContentReload()
        (moduleOf[file] as KaSourceModule).publishModuleOutOfBlockModificationEvent()
        return file
    }
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
    val tSession = measureTimeMillis { ws = Workspace(buildSession(model, args[1])) }
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

    fun save(file: File, text: String, psi: KtFile, label: String, check: (List<Diag>) -> String): KtFile {
        file.writeText(text)
        lateinit var fresh: KtFile
        lateinit var ds: List<Diag>
        val t = measureTimeMillis {
            fresh = ws.saved(psi)
            ds = diagnose(fresh)
        }
        println("save: %-44s %4dms  %s".format(label, t, check(ds)))
        return fresh
    }

    // 1. An error introduced and fixed in one file.
    var aPsi = save(aPath, aText + "\nval ktplsProbe: Int = \"not an int\"\n", a, "A with a type error") { ds ->
        "errors=" + errorsOf(ds).map { it.factory }
    }
    aPsi = save(aPath, aText, aPsi, "A fixed") { ds -> "errors=" + errorsOf(ds).map { it.factory } }

    // 2. Across files: B calls a function A doesn't declare yet...
    var bPsi = save(bPath, bText + "\nfun ktplsUse() = ktplsNewFunction()\n", b, "B calls a missing function") { ds ->
        "errors=" + errorsOf(ds).map { it.factory }
    }
    // ...then A is saved declaring it: B's error must go away.
    aPsi = save(aPath, aText + "\nfun ktplsNewFunction() = 1\n", aPsi, "A declares it") { ds -> "errors in A=" + errorsOf(ds).map { it.factory } }
    val tB = measureTimeMillis { bPsi = ws.file(args[3]) }
    val bErrors = errorsOf(diagnose(bPsi))
    println("       B re-analyzed after A's save:               errors=${bErrors.map { it.factory }}")

}
