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

data class Diag(val severity: KaSeverity, val factory: String, val message: String, val start: Int, val end: Int)

@OptIn(KaExperimentalApi::class)
fun diagnose(file: KtFile): List<Diag> = analyze(file) {
    file.collectDiagnostics(KaDiagnosticCheckerFilter.ONLY_COMMON_CHECKERS)
        .map {
            val r = it.textRanges.firstOrNull() ?: it.psi.textRange
            Diag(it.severity, it.factoryName, it.defaultMessage, r.startOffset, r.endOffset)
        }
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
    fun check(file: KtFile, text: String): List<Diag> = diagnose(copyOf(file, text))

    // copyOf returns new text of a file as an in-memory copy in its module.
    fun copyOf(file: KtFile, text: String): KtFile {
        val copy = KtPsiFactory(session.project).createFile(file.name, text)
        copy.contextModule = moduleOf.getValue(file)
        return copy
    }

    fun close() = Disposer.dispose(disposable)
}

