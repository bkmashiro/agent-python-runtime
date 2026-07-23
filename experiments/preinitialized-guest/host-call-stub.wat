(module
  (memory (export "memory") 1)
  (func (export "host_call")
    (param i32 i32 i32 i32)
    (result i32)
    i32.const -1))
