import numpy as np

a = np.array([[1, 2], [3, 4]])
result = {"product": (a @ a).tolist(), "sum": int(a.sum())}
