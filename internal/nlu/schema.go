package nlu

const Schema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["operation", "confidence", "transactions", "query", "snapshot"],
  "properties": {
    "operation": {"type": "string", "enum": ["add_transactions", "stats_query", "list_transactions", "snapshot", "noop"]},
    "confidence": {"type": "number"},
    "transactions": {
      "type": "array", "maxItems": 10,
      "items": {
        "type": "object", "additionalProperties": false,
        "required": ["amount", "currency", "date", "category", "description"],
        "properties": {
          "amount": {"type": "string"},
          "currency": {"type": "string"},
          "date": {"type": "string"},
          "category": {"type": "string"},
          "description": {"type": "string"}
        }
      }
    },
    "query": {
      "type": ["object", "null"], "additionalProperties": false,
      "required": ["metric", "from", "to", "group_by", "category", "user", "limit"],
      "properties": {
        "metric": {"type": ["string", "null"]},
        "from": {"type": ["string", "null"]},
        "to": {"type": ["string", "null"]},
        "group_by": {"type": ["string", "null"]},
        "category": {"type": ["string", "null"]},
        "user": {"type": ["string", "null"]},
        "limit": {"type": ["integer", "null"]}
      }
    },
    "snapshot": {
      "type": ["array", "null"], "maxItems": 10,
      "items": {
        "type": "object", "additionalProperties": false,
        "required": ["account", "user", "amount", "currency", "date"],
        "properties": {
          "account": {"type": "string"},
          "user": {"type": ["string", "null"]},
          "amount": {"type": "string"},
          "currency": {"type": "string"},
          "date": {"type": "string"}
        }
      }
    }
  }
}`
