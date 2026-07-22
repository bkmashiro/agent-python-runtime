#include <Python.h>

extern PyObject *PyInit__multiarray_umath(void);

/*
 * Link-only probe. The workflow never executes this export. Keeping the exact
 * initializer reference forces the static NumPy core and its CPython API
 * dependencies through the real wasm-ld monolithic link.
 */
__attribute__((export_name("numpy_link_probe")))
PyObject *numpy_link_probe(void) {
    return PyInit__multiarray_umath();
}
