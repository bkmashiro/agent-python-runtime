#include <Python.h>

extern PyObject *PyInit__multiarray_umath(void);

/*
 * Registration-only probe. PyImport_AppendInittab records the exact initializer
 * pointer before interpreter initialization; it does not execute NumPy or import
 * any module. The workflow invokes this export only after the reactor's C runtime
 * initializer has run.
 */
__attribute__((export_name("numpy_register_probe")))
int numpy_register_probe(void) {
    return PyImport_AppendInittab("numpy._core._multiarray_umath",
                                 PyInit__multiarray_umath);
}
