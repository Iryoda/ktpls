package acme.shapes

fun String./*@def(shout)*/shout(): String = this + "!"

val String./*@def(twice)*/twice: String
    get() = this + this

fun Square./*@def(Square.grow)*/grow(by: Double): Square {
    val bigger = /*@ref(Square)*/Square(/*@ref(Square.side)*/side + by)
    return bigger.scaled(/*@def(scaled.factor)*/factor = 1.0)
}

fun Square./*@def(scaled)*/scaled(factor: Double) = Square(this./*@ref(Square.side)*/side * factor)

class Point(val /*@def(Point.x)*/x: Int, val /*@def(Point.y)*/y: Int)

fun make(/*@def(make.label)*/label: String, /*@def(make.count)*/count: Int = 0) = label.repeat(count)

fun useExtensions(s: String, sq: Square) {
    s./*@ref(shout)*/shout()
    s./*@ref(twice)*/twice
    s./*@ref()*/length
    "lit"./*@ref(shout)*/shout()
    sq./*@ref(Square.grow)*/grow(1.0)
    Point(/*@ref(Point.x)*/x = 1, /*@ref(Point.y)*/y = 2)
    make(/*@ref(make.count)*/count = 2, /*@ref(make.label)*/label = "a")
}

fun scopes(p: Point) {
    p.apply { println(/*@ref(Point.x)*/x) }
    with(p) { println(/*@ref(Point.y)*/y) }
    p.run { println(this./*@ref(Point.x)*/x) }
    libraryDsl { /*@ref()*/area() } // lambda with an unknown library receiver
}
