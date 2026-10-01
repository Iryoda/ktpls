import com.intellij.psi.util.PsiTreeUtil
import org.jetbrains.kotlin.analysis.api.KaExperimentalApi
import org.jetbrains.kotlin.analysis.api.KaSession
import org.jetbrains.kotlin.analysis.api.analyze
import org.jetbrains.kotlin.analysis.api.components.KaDeprecationLevel
import org.jetbrains.kotlin.analysis.api.components.KaExtensionApplicabilityResult
import org.jetbrains.kotlin.analysis.api.components.KaScopeKind
import org.jetbrains.kotlin.analysis.api.components.KaScopeWithKind
import org.jetbrains.kotlin.analysis.api.components.KaUseSiteVisibilityChecker
import org.jetbrains.kotlin.analysis.api.signatures.KaCallableSignature
import org.jetbrains.kotlin.analysis.api.symbols.KaCallableSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaClassKind
import org.jetbrains.kotlin.analysis.api.symbols.KaClassSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaClassifierSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaConstructorSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaDeclarationSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaEnumEntrySymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaFunctionSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaSymbol
import org.jetbrains.kotlin.analysis.api.symbols.pointers.KaSymbolPointer
import org.jetbrains.kotlin.analysis.api.symbols.KaNamedClassSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaNamedFunctionSymbol
import org.jetbrains.kotlin.analysis.api.symbols.markers.KaNamedSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaTypeAliasSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaTypeParameterSymbol
import org.jetbrains.kotlin.analysis.api.types.KaErrorType
import org.jetbrains.kotlin.analysis.api.types.KaType
import org.jetbrains.kotlin.idea.references.mainReference
import org.jetbrains.kotlin.name.Name
import org.jetbrains.kotlin.psi.KtCallExpression
import org.jetbrains.kotlin.psi.KtExpression
import org.jetbrains.kotlin.psi.KtFile
import org.jetbrains.kotlin.psi.KtQualifiedExpression
import org.jetbrains.kotlin.psi.KtSimpleNameExpression
import org.jetbrains.org.objectweb.asm.AnnotationVisitor
import org.jetbrains.org.objectweb.asm.ClassReader
import org.jetbrains.org.objectweb.asm.ClassVisitor
import org.jetbrains.org.objectweb.asm.Opcodes
import java.util.zip.ZipFile

// Completion candidates the compiler knows and the workspace index
// doesn't: members and extensions from libraries (the stdlib's map,
// flatMap, getOrNull...) on the receiver's inferred type, and, without a
// receiver, what the scope offers (implicit receivers, default and
// explicit imports).

data class Candidate(
    val name: String,
    val kind: String,      // function, property, class, interface, object, enum, enumEntry, typeAlias
    val signature: String,
    val receiver: String?, // an extension's receiver type
    val container: String?, // the declaring class or package
    val member: Boolean,   // a member of the receiver (explicit or implicit), not an extension or import
    val id: String = "",   // for completionDoc: "generation:index"
)

// A placeholder identifier at the cursor, so that `xs.` parses as a
// reference to complete (as IntelliJ does).
private const val DUMMY = "ktplsCompletionDummy"

// Candidates beyond this are dropped: the client filters as the user types
// and asks again.
private const val MAX_CANDIDATES = 2000

@OptIn(KaExperimentalApi::class)
fun complete(ws: Workspace, original: KtFile, text: String, offset: Int, prefix: String): List<Candidate> {
    val file = ws.copyOf(original, text.substring(0, offset) + DUMMY + text.substring(offset))
    val leaf = file.findElementAt(offset) ?: return emptyList()
    val name = PsiTreeUtil.getParentOfType(leaf, KtSimpleNameExpression::class.java, false) ?: return emptyList()
    val match = nameMatcher(prefix)
    val generation = ++generations
    return analyze(file) {
        val out = Collector(match, generation)
        val receiver = receiverOf(name)
        val visibility = createUseSiteVisibilityChecker(file.symbol, receiver, name)
        if (receiver != null) receiverCandidates(file, name, receiver, visibility, out)
        else scopeCandidates(file, name, prefix, visibility, out)
        last = LastCompletion(generation, file, out.pointers)
        out.items
    }
}

// receiverOf returns the explicit receiver of the reference being
// completed: xs in `xs.ma|` or `xs?.ma|()`.
private fun receiverOf(name: KtSimpleNameExpression): KtExpression? {
    val selector = (name.parent as? KtCallExpression)?.takeIf { it.calleeExpression == name } ?: name
    val q = selector.parent as? KtQualifiedExpression ?: return null
    return q.receiverExpression.takeIf { q.selectorExpression == selector }
}

private class Collector(val match: (Name) -> Boolean, val generation: Int) {
    val items = mutableListOf<Candidate>()
    val pointers = mutableListOf<KaSymbolPointer<KaSymbol>>()
    val seen = HashSet<String>()
    fun add(c: Candidate, symbol: KaSymbol) {
        if (items.size < MAX_CANDIDATES && seen.add(c.name + "\u0000" + c.signature)) {
            items += c.copy(id = "$generation:${items.size}")
            pointers += symbol.createPointer()
        }
    }
}

// The last completion's symbols, for their docs: completionDoc restores
// one (by its candidate's id) in the file copy it was completed in.
private class LastCompletion(val generation: Int, val file: KtFile, val pointers: List<KaSymbolPointer<KaSymbol>>)

@Volatile private var last: LastCompletion? = null
private var generations = 0

// completionDoc returns the docs of a candidate of the last completion,
// found like a hover's: in the project, or the library's sources jar.
fun completionDoc(id: String): FoundSource? {
    val (gen, index) = id.split(":").mapNotNull { it.toIntOrNull() }.takeIf { it.size == 2 } ?: return null
    val l = last?.takeIf { it.generation == gen } ?: return null
    val pointer = l.pointers.getOrNull(index) ?: return null
    return analyze(l.file) {
        val restored = pointer.restoreSymbol() ?: return@analyze null
        val symbol = (restored as? KaCallableSymbol)?.fakeOverrideOriginal ?: restored
        findSource(l.file, symbol)
    }
}

// nameMatcher accepts names containing the prefix's characters in order,
// ignoring case: a superset of what the client's fuzzy matching keeps.
private fun nameMatcher(prefix: String): (Name) -> Boolean {
    if (prefix.isEmpty()) return { !it.isSpecial }
    val p = prefix.lowercase()
    return fun(n: Name): Boolean {
        if (n.isSpecial) return false
        val s = n.asString()
        if (!s[0].equals(p[0], ignoreCase = true)) return false
        var i = 0
        for (ch in s) if (i < p.length && ch.lowercaseChar() == p[i]) i++
        return i == p.length
    }
}

@OptIn(KaExperimentalApi::class)
private fun KaSession.receiverCandidates(
    file: KtFile, name: KtSimpleNameExpression, receiver: KtExpression,
    visibility: KaUseSiteVisibilityChecker, out: Collector,
) {
    // A class name: its static members, nested classes and companion.
    val cls = (receiver as? KtSimpleNameExpression)?.mainReference?.resolveToSymbol() as? KaClassSymbol
    if (cls != null && cls.classKind != KaClassKind.OBJECT && cls.classKind != KaClassKind.COMPANION_OBJECT) {
        for (s in cls.staticMemberScope.callables(out.match)) addSymbol(s, null, true, visibility, out)
        for (s in cls.staticMemberScope.classifiers(out.match)) addClassifier(s, visibility, out)
        for (s in cls.staticDeclaredMemberScope.classifiers(out.match)) addClassifier(s, visibility, out)
        (cls as? KaNamedClassSymbol)?.companionObject?.let { companion ->
            for (s in companion.memberScope.callables(out.match)) addSymbol(s, null, true, visibility, out)
        }
        return
    }
    val type = receiver.expressionType ?: return
    if (type is KaErrorType) return // everything would be applicable
    members(type, visibility, out)
    val checker = createExtensionCandidateChecker(file, name, receiver)
    for (scope in file.scopeContext(name).scopes) {
        for (s in callables(scope, out.match)) {
            if (!s.isExtension) continue
            val applicable = checker.computeApplicability(s) as? KaExtensionApplicabilityResult.Applicable ?: continue
            val sig = (applicable as? KaExtensionApplicabilityResult.ApplicableAsExtensionCallable)
                ?.let { s.substitute(it.substitutor) }
            addSymbol(s, sig, false, visibility, out)
        }
    }
}

// members offers the members of type, Java getters as properties included.
@OptIn(KaExperimentalApi::class)
private fun KaSession.members(type: KaType, visibility: KaUseSiteVisibilityChecker, out: Collector) {
    val t = type.withNullability(false)
    t.scope?.getCallableSignatures(out.match)?.forEach { addSymbol(it.symbol, it, true, visibility, out) }
    t.syntheticJavaPropertiesScope?.getCallableSignatures(out.match)?.forEach { addSymbol(it.symbol, it, true, visibility, out) }
}

@OptIn(KaExperimentalApi::class)
private fun KaSession.scopeCandidates(
    file: KtFile, name: KtSimpleNameExpression, prefix: String,
    visibility: KaUseSiteVisibilityChecker, out: Collector,
) {
    val context = file.scopeContext(name)
    // Implicit receivers: `this` in a class, a lambda with receiver (apply,
    // with, buildList...).
    for (r in context.implicitReceivers) members(r.type, visibility, out)
    // Imports, default ones included, and the package. Locals and the
    // enclosing classes' own members come from ktpls; without anything
    // typed there would be thousands.
    if (prefix.isEmpty()) return
    for (scope in context.scopes) {
        when (scope.kind) {
            is KaScopeKind.LocalScope, is KaScopeKind.TypeParameterScope -> continue
            else -> {}
        }
        for (s in callables(scope, out.match)) {
            if (!s.isExtension) addSymbol(s, null, false, visibility, out)
        }
        for (s in classifiers(scope, out.match)) addClassifier(s, visibility, out)
    }
}

// Scopes over library packages (imports, default ones included) can't
// list their names, only look them up: their names come from LibraryNames.
private fun callables(scope: KaScopeWithKind, match: (Name) -> Boolean): Sequence<KaCallableSymbol> =
    if (!overPackages(scope)) scope.scope.callables(match)
    else scope.scope.callables(match) + scope.scope.callables(LibraryNames.callables().filter(match))

private fun classifiers(scope: KaScopeWithKind, match: (Name) -> Boolean): Sequence<KaClassifierSymbol> =
    if (!overPackages(scope)) scope.scope.classifiers(match)
    else scope.scope.classifiers(match) + scope.scope.classifiers(LibraryNames.classifiers().filter(match))

private fun overPackages(scope: KaScopeWithKind) =
    scope.kind is KaScopeKind.ImportingScope || scope.kind is KaScopeKind.PackageMemberScope

// LibraryNames holds the names of the libraries' top-level declarations:
// functions and properties from the Kotlin metadata of file facades (its
// d2 string table, a superset of the names declared), classes from the
// class files.
object LibraryNames {
    @Volatile private var callables: Set<Name> = emptySet()
    @Volatile private var classifiers: Set<Name> = emptySet()

    fun callables() = callables
    fun classifiers() = classifiers

    // index reads the jars' names in the background.
    fun index(jars: List<String>) {
        Thread {
            val fns = HashSet<Name>()
            val classes = HashSet<Name>()
            val ms = kotlin.system.measureTimeMillis {
                for (jar in jars) read(jar, fns, classes)
            }
            callables = fns
            classifiers = classes
            System.err.println("analyzer: indexed ${fns.size} library callable and ${classes.size} class names in ${ms}ms")
        }.apply { isDaemon = true; name = "ktpls-library-names" }.start()
    }

    private val identifier = Regex("""[A-Za-z_][A-Za-z0-9_]*""")

    private fun read(jar: String, fns: MutableSet<Name>, classes: MutableSet<Name>) = try {
        ZipFile(jar).use { z ->
            val entries = z.entries().toList()
            val kotlin = entries.any { it.name.startsWith("META-INF/") && it.name.endsWith(".kotlin_module") }
            for (e in entries) {
                if (!e.name.endsWith(".class") || e.name.endsWith("module-info.class")) continue
                val simple = e.name.substringAfterLast('/').removeSuffix(".class")
                if ('$' in simple) continue // nested: reached through their outer class
                if (identifier.matches(simple)) classes += Name.identifier(simple)
                if (kotlin && simple.contains("Kt")) metadataNames(z.getInputStream(e).readBytes(), fns)
            }
        }
    } catch (_: Exception) {
    }

    // metadataNames adds the d2 strings of a file facade (k=2) or a
    // multifile class part (k=5).
    private fun metadataNames(bytes: ByteArray, out: MutableSet<Name>) {
        var kind = 0
        val d2 = mutableListOf<String>()
        try {
            ClassReader(bytes).accept(object : ClassVisitor(Opcodes.API_VERSION) {
                override fun visitAnnotation(desc: String, visible: Boolean): AnnotationVisitor? {
                    if (desc != "Lkotlin/Metadata;") return null
                    return object : AnnotationVisitor(Opcodes.API_VERSION) {
                        override fun visit(name: String?, value: Any?) { if (name == "k") kind = value as? Int ?: 0 }
                        override fun visitArray(name: String?): AnnotationVisitor? = if (name != "d2") null else
                            object : AnnotationVisitor(Opcodes.API_VERSION) {
                                override fun visit(n: String?, value: Any?) { (value as? String)?.let { d2 += it } }
                            }
                    }
                }
            }, ClassReader.SKIP_CODE or ClassReader.SKIP_DEBUG or ClassReader.SKIP_FRAMES)
        } catch (_: Exception) {
            return
        }
        if (kind != 2 && kind != 5) return
        for (s in d2) if (identifier.matches(s)) out += Name.identifier(s)
    }
}

@OptIn(KaExperimentalApi::class)
private fun KaSession.addSymbol(
    s: KaCallableSymbol, sig: KaCallableSignature<*>?, member: Boolean,
    visibility: KaUseSiteVisibilityChecker, out: Collector,
) {
    if (s is KaConstructorSymbol || dataComponent(s) || !visible(s, visibility)) return
    val name = s.callableId?.callableName?.asString() ?: (s as? KaNamedSymbol)?.name?.asString() ?: return
    val signature = sig ?: s.asSignature()
    out.add(Candidate(
        name = name,
        kind = if (s is KaFunctionSymbol) "function" else "property",
        signature = renderCall(name, signature),
        receiver = signature.receiverType?.let { renderType(it) },
        container = s.callableId?.let { it.classId?.shortClassName?.asString() ?: it.packageName.asString() }?.takeIf { it.isNotEmpty() },
        member = member,
    ), s)
}

private val componentName = Regex("""component\d+""")

// dataComponent reports whether s is a data class's generated componentN,
// for destructuring: not called by name, so not offered (as in IntelliJ).
// Explicit operator functions, like Map.Entry's component1, are.
private fun KaSession.dataComponent(s: KaCallableSymbol): Boolean {
    if (s !is KaNamedFunctionSymbol || s.isExtension || !componentName.matches(s.name.asString())) return false
    return (s.fakeOverrideOriginal.containingDeclaration as? KaNamedClassSymbol)?.isData == true
}

private fun KaSession.addClassifier(s: KaClassifierSymbol, visibility: KaUseSiteVisibilityChecker, out: Collector) {
    if (s is KaTypeParameterSymbol || !visible(s, visibility)) return
    val name = s.name?.asString() ?: return
    val kind = when (s) {
        is KaTypeAliasSymbol -> "typeAlias"
        is KaClassSymbol -> when (s.classKind) {
            KaClassKind.INTERFACE -> "interface"
            KaClassKind.ENUM_CLASS -> "enum"
            KaClassKind.OBJECT, KaClassKind.COMPANION_OBJECT -> "object"
            else -> "class"
        }
        else -> return
    }
    val classId = s.classId
    out.add(Candidate(
        name = name, kind = kind,
        signature = renderDeclaration(s),
        receiver = null,
        container = classId?.let { it.outerClassId?.shortClassName?.asString() ?: it.packageFqName.asString() }?.takeIf { it.isNotEmpty() },
        member = false,
    ), s)
}

private fun KaSession.visible(s: KaDeclarationSymbol, visibility: KaUseSiteVisibilityChecker): Boolean {
    if (s is KaEnumEntrySymbol) return true
    if (s.deprecation?.level == KaDeprecationLevel.HIDDEN) return false
    return visibility.isVisible(s)
}
