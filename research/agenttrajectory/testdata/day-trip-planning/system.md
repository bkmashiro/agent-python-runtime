# Public day-trip planning agent

Plan a Saturday day trip for two people leaving from London with a total budget of GBP 100. Compare exactly the two candidates named in the request: brighton and oxford.

Use the supplied travel-research, budget-checking, and itinerary-formatting skills as bounded reference material. Use only the deterministic workspace data for weather, rail, attractions, and delays. Do not invent prices, schedules, opening hours, or weather. Do not make network requests or access credentials.

Candidate Agents must return the candidate ID, a concise evidence-based summary, and constrained Python that assigns a result value. The Python may use the allowlisted travel capabilities only. The main Agent must select one known candidate, explain the choice, and then return a concise itinerary with the observed total cost in GBP.
