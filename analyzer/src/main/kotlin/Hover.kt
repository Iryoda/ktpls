import com.intellij.lang.java.JavaLanguage
import com.intellij.openapi.project.Project
import com.intellij.psi.PsiClass
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiField
import com.intellij.psi.PsiFileFactory
import com.intellij.psi.PsiJavaDocumentedElement
import com.intellij.psi.PsiJavaFile
import com.intellij.psi.PsiMethod
import com.intellij.psi.util.PsiTreeUtil
import org.jetbrains.kotlin.analysis.api.KaExperimentalApi
import org.jetbrains.kotlin.analysis.api.KaSession
import org.jetbrains.kotlin.analysis.api.analyze
import org.jetbrains.kotlin.analysis.api.renderer.base.annotations.KaRendererAnnotationsFilter
import org.jetbrains.kotlin.analysis.api.renderer.declarations.impl.KaDeclarationRendererForSource
import org.jetbrains.kotlin.analysis.api.renderer.types.impl.KaTypeRendererForSource
import org.jetbrains.kotlin.analysis.api.renderer.types.KaTypeRenderer
import org.jetbrains.kotlin.analysis.api.renderer.types.renderers.KaFlexibleTypeRenderer
import org.jetbrains.kotlin.analysis.api.symbols.KaConstructorSymbol
import org.jetbrains.kotlin.analysis.api.types.KaFlexibleType
import org.jetbrains.kotlin.analysis.utils.printer.PrettyPrinter
import org.jetbrains.kotlin.analysis.api.resolution.KaCallResolutionSuccess
import org.jetbrains.kotlin.analysis.api.signatures.KaFunctionSignature
import org.jetbrains.kotlin.analysis.api.signatures.KaVariableSignature
import org.jetbrains.kotlin.analysis.api.projectStructure.KaLibraryModule
import org.jetbrains.kotlin.analysis.api.symbols.KaCallableSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaClassLikeSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaDeclarationSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaFunctionSymbol
import org.jetbrains.kotlin.analysis.api.symbols.KaSymbol
import org.jetbrains.kotlin.analysis.api.types.KaType
import org.jetbrains.kotlin.idea.references.mainReference
import org.jetbrains.kotlin.psi.KtCallableDeclaration
import org.jetbrains.kotlin.psi.KtClassOrObject
import org.jetbrains.kotlin.psi.KtDeclaration
import org.jetbrains.kotlin.psi.KtFile
import org.jetbrains.kotlin.psi.KtNameReferenceExpression
import org.jetbrains.kotlin.psi.KtNamedDeclaration
import org.jetbrains.kotlin.psi.KtNamedFunction
import org.jetbrains.kotlin.psi.KtPsiFactory
import org.jetbrains.kotlin.types.Variance
import org.jetbrains.org.objectweb.asm.AnnotationVisitor
import org.jetbrains.org.objectweb.asm.ClassReader
import org.jetbrains.org.objectweb.asm.ClassVisitor
import org.jetbrains.org.objectweb.asm.Opcodes
import java.io.File
import java.util.zip.ZipFile

// Hover information about the reference at an offset: what it resolves to
// with the compiler's front end, and the declaration's documentation from
// the library's sources jar (or the project's source).

data class HoverInfo(
    val start: Int, val end: Int, // the reference, in UTF-16 units
    val signature: String,        // the declaration
    val call: String?,            // with this call's type arguments, if they differ
    val container: String?,       // class or package
    val doc: String?,             // the raw doc comment
    val docLanguage: String?,     // "kotlin" or "java"
    val source: SourceLocation?,  // where the declaration is
)

// A declaration in a project file (jar null) or in a sources jar entry.
data class SourceLocation(val path: String, val jar: String?, val entry: String?, val offset: Int)

@OptIn(KaExperimentalApi::class)
fun hover(file: KtFile, offset: Int): HoverInfo? = analyze(file) {
    val leaf = file.findElementAt(offset) ?: return@analyze null
    val ref = PsiTreeUtil.getParentOfType(leaf, KtNameReferenceExpression::class.java, false)
        ?: return@analyze declarationHover(leaf)
    val call = (ref.tryResolveCall() as? KaCallResolutionSuccess)?.call
    val resolved: KaSymbol = call?.signature?.symbol ?: ref.mainReference.resolveToSymbol() ?: return@analyze null
    // An inherited member (e.g. a repository's save): describe the original.
    val symbol = (resolved as? KaCallableSymbol)?.fakeOverrideOriginal ?: resolved
    val decl = symbol as? KaDeclarationSymbol ?: return@analyze null
    val signature = renderDeclaration(decl)
    val callText = call?.signature?.let { renderCall(ref.getReferencedName(), it) }
    val declared = (symbol as? KaCallableSymbol)?.let { renderCall(ref.getReferencedName(), it.asSignature()) }
    val source = findSource(file.project, symbol)
    HoverInfo(
        start = ref.textRange.startOffset, end = ref.textRange.endOffset,
        signature = signature,
        call = callText?.takeIf { it != declared },
        container = containerOf(symbol),
        doc = source?.doc, docLanguage = source?.language,
        source = source?.location,
    )
}

// declarationHover describes the declaration whose name is at leaf (e.g.
// a lambda parameter, with its inferred type).
private fun KaSession.declarationHover(leaf: PsiElement): HoverInfo? {
    val decl = leaf.parent as? KtNamedDeclaration ?: return null
    if (decl.nameIdentifier != leaf) return null
    val symbol = decl.symbol
    return HoverInfo(
        start = leaf.textRange.startOffset, end = leaf.textRange.endOffset,
        signature = renderDeclaration(symbol),
        call = null,
        container = containerOf(symbol),
        doc = decl.docComment?.text, docLanguage = "kotlin",
        source = null,
    )
}

// Everything is rendered as valid Kotlin, since editors highlight hovers
// by parsing them.

// A Java type as Kotlin code sees it: MutableList<T>, not
// kotlin.collections.(Mutable)List<T>!.
private object LowerBound : KaFlexibleTypeRenderer {
    override fun renderType(analysisSession: KaSession, type: KaFlexibleType, typeRenderer: KaTypeRenderer, printer: PrettyPrinter) {
        typeRenderer.renderType(analysisSession, type.lowerBound, printer)
    }
}

private val hoverTypeRenderer = KaTypeRendererForSource.WITH_SHORT_NAMES.with { flexibleTypeRenderer = LowerBound }

// Declarations without their annotations (@InlineOnly, @SinceKotlin...).
private val declarationRenderer = KaDeclarationRendererForSource.WITH_SHORT_NAMES.with {
    typeRenderer = hoverTypeRenderer
    annotationRenderer = annotationRenderer.with { annotationFilter = KaRendererAnnotationsFilter.NONE }
}

private fun KaSession.render(t: KaType) = t.render(hoverTypeRenderer, Variance.INVARIANT)

// renderDeclaration renders a declaration; a constructor as the function
// it is: fun <T> Box(value: T): Box<T>.
private fun KaSession.renderDeclaration(symbol: KaDeclarationSymbol): String {
    if (symbol is KaConstructorSymbol) {
        val name = symbol.containingClassId?.shortClassName?.asString() ?: "constructor"
        val params = symbol.typeParameters.map { it.name.asString() }
        val typeParams = if (params.isEmpty()) "" else params.joinToString(", ", "<", "> ")
        return renderCall(name, symbol.asSignature()).replaceFirst("fun ", "fun $typeParams")
    }
    return symbol.render(declarationRenderer)
}

// renderCall renders a signature as `fun Receiver.name(p: T): R` (or
// `val Receiver.name: R`).
private fun KaSession.renderCall(name: String, sig: org.jetbrains.kotlin.analysis.api.signatures.KaCallableSignature<*>): String {
    val receiver = sig.receiverType?.let { render(it) + "." } ?: ""
    return when (sig) {
        is KaFunctionSignature<*> -> "fun " + receiver + name + sig.valueParameters.joinToString(", ", "(", ")") {
            "${it.name.asString()}: ${render(it.returnType)}"
        } + ": " + render(sig.returnType)
        is KaVariableSignature<*> -> "val " + receiver + name + ": " + render(sig.returnType)
        else -> receiver + name
    }
}

private fun containerOf(symbol: KaSymbol): String? = when (symbol) {
    is KaConstructorSymbol -> symbol.containingClassId?.asFqNameString()
    is KaCallableSymbol -> symbol.callableId?.let { it.classId?.asFqNameString() ?: it.packageName.asString() }
    is KaClassLikeSymbol -> symbol.classId?.let { it.outerClassId?.asFqNameString() ?: it.packageFqName.asString() }
    else -> null
}?.takeIf { it.isNotEmpty() }

class FoundSource(val location: SourceLocation, val doc: String?, val language: String)

// findSource finds a declaration's source: in the project, or in the
// sources jar next to the library jar holding its class.
private fun KaSession.findSource(project: Project, symbol: KaSymbol): FoundSource? {
    val psi = symbol.psi
    val vf = psi?.containingFile?.virtualFile
    if (psi != null && vf != null && !vf.path.contains("!/")) { // a project file
        val doc = (psi as? KtDeclaration)?.docComment?.text ?: (psi as? PsiJavaDocumentedElement)?.docComment?.text
        return FoundSource(SourceLocation(vf.path, null, null, psi.textOffset), doc, if (vf.path.endsWith(".java")) "java" else "kotlin")
    }
    // The class file: from the PSI (Java classes), or found in the
    // library's jars by its JVM name (Kotlin declarations have no PSI).
    val (jarPath, entry) = if (vf != null) {
        vf.path.split("!/", limit = 2).let { it[0] to it[1] }
    } else {
        val cls = jvmClassOf(symbol) ?: return null
        val entry = "$cls.class"
        val roots = (symbol.containingModule as? KaLibraryModule)?.binaryRoots.orEmpty().map { it.toString() }
        (roots.firstOrNull { Sources.has(it, entry) } ?: return null) to entry
    }
    val bytes = Sources.readBytes(jarPath, entry) ?: return null
    val sources = Sources.jarFor(jarPath) ?: return null
    val info = classInfo(bytes)
    // A multifile facade (e.g. CollectionsKt) has the declarations in parts.
    val sourceFiles = info.parts.mapNotNull { part -> Sources.readBytes(jarPath, "$part.class")?.let { classInfo(it).sourceFile } } +
        listOfNotNull(info.sourceFile ?: entry.substringAfterLast('/').substringBefore('$').removeSuffix(".class") + ".java")
    val pkgDir = entry.substringBeforeLast('/', "")
    val name = when (symbol) {
        is KaCallableSymbol -> symbol.callableId?.callableName?.asString()
        is KaClassLikeSymbol -> symbol.classId?.shortClassName?.asString()
        else -> null
    } ?: return null
    val owner = (symbol as? KaCallableSymbol)?.callableId?.classId?.relativeClassName?.pathSegments()?.map { it.asString() }
        ?: (symbol as? KaClassLikeSymbol)?.classId?.relativeClassName?.pathSegments()?.dropLast(1)?.map { it.asString() }.orEmpty()
    val params = (symbol as? KaFunctionSymbol)?.valueParameters?.map { it.name.asString() }
    val extension = (symbol as? KaCallableSymbol)?.isExtension == true
    var fallback: FoundSource? = null
    for (candidate in sourceFiles.distinct().flatMap { Sources.entries(sources, it, pkgDir) }) {
        val text = Sources.read(sources, candidate) ?: continue
        val found = if (candidate.endsWith(".java")) javaDeclaration(project, candidate, text, owner, name, params)
        else kotlinDeclaration(project, candidate, text, owner, name, params, extension)
        found ?: continue
        val doc = (found as? KtDeclaration)?.docComment?.text ?: (found as? PsiJavaDocumentedElement)?.docComment?.text
        val result = FoundSource(SourceLocation(sources, sources, candidate, found.textOffset), doc,
            if (candidate.endsWith(".java")) "java" else "kotlin")
        if (doc != null) return result // e.g. the expect declaration has the docs, not the actual one
        if (fallback == null) fallback = result
    }
    return fallback
}

// jvmClassOf returns the internal name of the class file holding a
// declaration, e.g. kotlin/ResultKt or java/util/Map$Entry.
private fun KaSession.jvmClassOf(symbol: KaSymbol): String? {
    val classId = when (symbol) {
        is KaCallableSymbol -> symbol.callableId?.classId
        is KaClassLikeSymbol -> symbol.classId
        else -> null
    }
    if (classId != null) {
        val pkg = classId.packageFqName.asString().replace('.', '/')
        val cls = classId.relativeClassName.asString().replace('.', '$')
        return if (pkg.isEmpty()) cls else "$pkg/$cls"
    }
    return (symbol as? KaCallableSymbol)?.containingJvmClassName?.replace('.', '/')
}

private class ClassInfo(val sourceFile: String?, val parts: List<String>)

// classInfo reads a class file's SourceFile attribute and, for a Kotlin
// multifile facade, its parts (the Metadata annotation's d1).
private fun classInfo(bytes: ByteArray): ClassInfo {
    var source: String? = null
    val parts = mutableListOf<String>()
    var kind = 0
    try {
        ClassReader(bytes).accept(object : ClassVisitor(Opcodes.API_VERSION) {
            override fun visitSource(s: String?, debug: String?) { source = s }
            override fun visitAnnotation(desc: String, visible: Boolean): AnnotationVisitor? {
                if (desc != "Lkotlin/Metadata;") return null
                return object : AnnotationVisitor(Opcodes.API_VERSION) {
                    override fun visit(name: String?, value: Any?) { if (name == "k") kind = value as? Int ?: 0 }
                    override fun visitArray(name: String?): AnnotationVisitor? = if (name != "d1") null else
                        object : AnnotationVisitor(Opcodes.API_VERSION) {
                            override fun visit(n: String?, value: Any?) { (value as? String)?.let { parts += it } }
                        }
                }
            }
        }, ClassReader.SKIP_CODE or ClassReader.SKIP_FRAMES)
    } catch (_: Exception) {
    }
    return ClassInfo(source, if (kind == 4) parts else emptyList()) // 4: a multifile class facade
}

// The declarations are matched by owner, name, and then by parameters:
// the same names first (overloads often differ only in types), else the
// same count.

private fun kotlinDeclaration(project: Project, name: String, text: String, owner: List<String>, member: String, params: List<String>?, extension: Boolean): PsiElement? {
    val file = KtPsiFactory(project, markGenerated = false).createFile(name.substringAfterLast('/'), text)
    val all = PsiTreeUtil.findChildrenOfType(file, KtNamedDeclaration::class.java).filter { it.name == member }
    val inOwner = all.filter { d -> classChain(d) == owner || (owner.isEmpty() && classChain(d).isEmpty()) }
    val candidates = inOwner.ifEmpty { all }
    val kind = candidates.filter { d -> d !is KtCallableDeclaration || (d.receiverTypeReference != null) == extension }.ifEmpty { candidates }
    fun names(d: KtNamedDeclaration) = (d as? KtNamedFunction)?.valueParameters?.map { it.name.orEmpty() }
    return kind.firstOrNull { params == null || names(it) == null || names(it) == params }
        ?: kind.firstOrNull { names(it)?.size == params?.size }
        ?: kind.firstOrNull()
}

private fun classChain(d: KtDeclaration): List<String> =
    generateSequence(PsiTreeUtil.getParentOfType(d, KtClassOrObject::class.java)) { PsiTreeUtil.getParentOfType(it, KtClassOrObject::class.java) }
        .mapNotNull { it.name }.toList().reversed()

private fun javaDeclaration(project: Project, name: String, text: String, owner: List<String>, member: String, params: List<String>?): PsiElement? {
    val file = PsiFileFactory.getInstance(project).createFileFromText(name.substringAfterLast('/'), JavaLanguage.INSTANCE, text) as? PsiJavaFile ?: return null
    fun find(classes: Array<PsiClass>, chain: List<String>): PsiClass? {
        val c = classes.firstOrNull { it.name == chain.first() } ?: return null
        return if (chain.size == 1) c else find(c.innerClasses, chain.drop(1))
    }
    if (owner.isEmpty()) return file.classes.firstOrNull { it.name == member }
    val cls = find(file.classes, owner) ?: return null
    fun best(methods: List<PsiMethod>): PsiMethod? {
        fun names(m: PsiMethod) = m.parameterList.parameters.map { it.name }
        return methods.firstOrNull { params == null || names(it) == params }
            ?: methods.firstOrNull { it.parameterList.parametersCount == params?.size }
            ?: methods.firstOrNull()
    }
    if (member == "<init>" || member == cls.name) return best(cls.constructors.toList()) ?: cls
    best(cls.findMethodsByName(member, false).toList())?.let { return it }
    // A Kotlin property of a Java getter: getName/isName.
    val cap = member.replaceFirstChar { it.uppercase() }
    cls.findMethodsByName("get$cap", false).firstOrNull()?.let { return it }
    cls.findMethodsByName("is$cap", false).firstOrNull()?.let { return it }
    return cls.findFieldByName(member, false) as PsiField? ?: cls.innerClasses.firstOrNull { it.name == member }
}

// Sources finds and reads libraries' sources jars.
object Sources {
    private val jars = HashMap<String, String>()
    private val indexes = HashMap<String, Map<String, List<String>>>()
    private val entryNames = HashMap<String, Set<String>>()

    // jarFor returns the sources jar of a library jar: next to it (Maven
    // layout), or in a sibling directory (Gradle's cache).
    // Misses aren't remembered: ktpls may download the sources meanwhile.
    fun jarFor(jar: String): String? = synchronized(this) {
        jars[jar]?.let { return it }
        val f = File(jar)
        val name = f.name.removeSuffix(".jar") + "-sources.jar"
        val beside = File(f.parentFile, name)
        val found = if (beside.isFile) beside.path
        else f.parentFile?.parentFile?.listFiles()?.map { File(it, name) }?.firstOrNull { it.isFile }?.path
        if (found != null) jars[jar] = found
        found
    }

    // entries returns the entries named file, those in dir first.
    fun entries(jar: String, file: String, dir: String): List<String> {
        val all = index(jar)[file].orEmpty()
        return all.sortedBy { if (it == "$dir/$file" || it.endsWith("/$dir/$file")) 0 else 1 }
    }

    private fun index(jar: String): Map<String, List<String>> = synchronized(this) {
        indexes.getOrPut(jar) {
            try {
                ZipFile(jar).use { z -> z.entries().toList().filter { !it.isDirectory }.map { it.name }.groupBy { it.substringAfterLast('/') } }
            } catch (_: Exception) {
                emptyMap()
            }
        }
    }

    fun has(jar: String, entry: String): Boolean = synchronized(this) {
        entryNames.getOrPut(jar) {
            try {
                ZipFile(jar).use { z -> z.entries().toList().map { it.name }.toHashSet() }
            } catch (_: Exception) {
                emptySet()
            }
        }.contains(entry)
    }

    fun readBytes(jar: String, entry: String): ByteArray? = try {
        ZipFile(jar).use { z -> z.getEntry(entry)?.let { e -> z.getInputStream(e).readBytes() } }
    } catch (_: Exception) {
        null
    }

    fun read(jar: String, entry: String): String? = try {
        ZipFile(jar).use { z -> z.getEntry(entry)?.let { e -> z.getInputStream(e).readBytes().toString(Charsets.UTF_8) } }
    } catch (_: Exception) {
        null
    }
}
