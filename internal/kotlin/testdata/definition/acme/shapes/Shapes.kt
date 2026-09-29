package acme.shapes

import acme.util.Logger
import acme.util.format as fmt
import acme.colors.*

interface /*@def(Shape)*/Shape {
    fun /*@def(Shape.area,anyArea)*/area(): Double
}

data class /*@def(Circle)*/Circle(val /*@def(Circle.r)*/r: Double, /*@def(scale)*/scale: Int) : /*@ref(Shape)*/Shape {
    val /*@def(Circle.diameter)*/diameter = /*@ref(Circle.r)*/r * 2 * /*@ref(scale)*/scale

    override fun /*@def(Circle.area,anyArea)*/area(): Double {
        val /*@def(local.pi)*/pi = /*@ref(PI)*/PI
        return /*@ref(local.pi)*/pi * /*@ref(Circle.r)*/r * r
    }

    fun /*@def(Circle.log)*/log(/*@def(param.msg)*/msg: String) {
        /*@ref(Logger)*/Logger./*@ref(Logger.info)*/info(/*@ref(param.msg)*/msg)
        /*@ref(fmt)*/fmt(msg)
        val c: /*@ref(Color)*/Color = /*@ref(Color)*/Color./*@ref(Color.RED)*/RED
        listOf(1, 2).forEach { /*@def(lambda.x)*/x -> println(/*@ref(lambda.x)*/x) }
        for (/*@def(loop.i)*/i in 0..3) {
            println(/*@ref(loop.i)*/i)
        }
        println(/*@ref(Circle.diameter)*/diameter + /*@ref(Circle.area)*/area())
        println(this./*@ref(Circle.diameter)*/diameter)
    }

    companion object {
        const val /*@def(PI)*/PI = 3.14

        fun unit() = /*@ref(Circle)*/Circle(1.0, 1)
    }
}

class /*@def(Square)*/Square(val /*@def(Square.side)*/side: Double) : /*@ref(Shape)*/Shape {
    override fun /*@def(Square.area,anyArea)*/area() = side * side
}

open class /*@def(Base)*/Base {
    fun /*@def(Base.greet)*/greet() = "hi"
}

class Sub : /*@ref(Base)*/Base() {
    fun f() = /*@ref(Base.greet)*/greet()
}

fun /*@def(total)*//*@ref(total)*/total(shapes: List</*@ref(Shape)*/Shape>): Double {
    val /*@def(local.c)*/c = /*@ref(Circle)*/Circle(2.0, 1)
    /*@ref(local.c)*/c./*@ref(Circle.log)*/log("x")
    val s: /*@ref(Square)*/Square = Square(1.0)
    s./*@ref(Square.area)*/area()
    /*@ref(extra)*/extra()
    /*@ref()*/helper() // not imported: not visible
    val q: acme.colors./*@ref(Color)*/Color = Color.GREEN
    Circle./*@ref(PI)*/PI
    return shapes.sumOf { it./*@ref(anyArea)*/area() }
}

fun shadow(v: Int): Int {
    val /*@def(shadow.inner)*/v = 2
    val f = { /*@def(shadow.lambda)*/v: Int -> /*@ref(shadow.lambda)*/v }
    return /*@ref(shadow.inner)*/v + f(1)
}

fun <T> identity(x: T): T = x

class Box</*@def(T)*/T>(val item: /*@ref(T)*/T)
