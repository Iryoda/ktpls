import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import org.jetbrains.kotlin.analysis.api.diagnostics.KaSeverity
import kotlin.system.exitProcess
import kotlin.system.measureTimeMillis

// The analyzer process: requests and responses are JSON objects, one per
// line, on stdin and stdout. Logs go to stderr.

class Analyzer {
    private var model: Model? = null
    private var jdkHome = ""
    private var ws: Workspace? = null
    private var byPath: Map<String, org.jetbrains.kotlin.psi.KtFile> = emptyMap()

    private fun open(): JsonObject {
        ws?.close()
        lateinit var fresh: Workspace
        val ms = measureTimeMillis { fresh = Workspace(model!!, jdkHome) }
        ws = fresh
        byPath = fresh.files.associateBy { it.virtualFile.path }
        return buildJsonObject {
            put("files", fresh.files.size)
            put("millis", ms)
        }
    }

    fun handle(method: String, params: JsonObject): JsonElement = when (method) {
        "init" -> {
            model = Model(params.string("model"))
            jdkHome = params.string("jdkHome")
            open()
        }
        "rebuild" -> open()
        "check" -> {
            val path = params.string("path")
            val file = byPath[path] ?: error("not in the project: $path")
            fileResult(path, ws!!.check(file, params.string("text")))
        }
        "diagnose" -> {
            val paths = params["paths"]?.let { if (it is JsonNull) null else it.jsonArray.map { p -> p.jsonPrimitive.content } }
            val files = if (paths.isNullOrEmpty()) byPath.values.toList() else paths.mapNotNull { byPath[it] }
            buildJsonObject {
                put("files", buildJsonArray {
                    for (f in files) {
                        val ds = diagnose(f)
                        if (ds.isNotEmpty()) add(fileResult(f.virtualFile.path, ds))
                    }
                })
            }
        }
        else -> error("unknown method $method")
    }

    private fun fileResult(path: String, ds: List<Diag>) = buildJsonObject {
        put("path", path)
        put("diagnostics", JsonArray(ds.map { d ->
            buildJsonObject {
                put("severity", when (d.severity) {
                    KaSeverity.ERROR -> "error"
                    KaSeverity.WARNING -> "warning"
                    else -> "info"
                })
                put("start", d.start)
                put("end", d.end)
                put("message", d.message)
                put("factory", d.factory)
            }
        }))
    }
}

private fun JsonObject.string(key: String) = this[key]?.jsonPrimitive?.content ?: error("missing $key")

fun main() {
    val analyzer = Analyzer()
    val out = System.out
    System.setOut(System.err) // nothing but responses may reach stdout
    val reader = System.`in`.bufferedReader()
    while (true) {
        val line = reader.readLine() ?: break
        if (line.isBlank()) continue
        val req = Json.parseToJsonElement(line).jsonObject
        val id = req["id"]?.jsonPrimitive?.int ?: 0
        val method = req["method"]?.jsonPrimitive?.content ?: ""
        if (method == "shutdown") break
        val params = (req["params"] as? JsonObject) ?: JsonObject(emptyMap())
        val resp = try {
            buildJsonObject {
                put("id", id)
                put("result", analyzer.handle(method, params))
            }
        } catch (t: Throwable) {
            System.err.println("analyzer: $method failed: $t")
            buildJsonObject {
                put("id", id)
                put("error", t.message ?: t.toString())
            }
        }
        out.println(resp.toString())
        out.flush()
    }
    exitProcess(0)
}
