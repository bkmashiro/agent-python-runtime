module_name = inputs["module"]
module = __import__(module_name)
result = module.__name__
