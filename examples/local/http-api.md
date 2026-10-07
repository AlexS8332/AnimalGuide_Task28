# HTTP API локальной модели — curl напрямую

Прогон 2026-10-07: Ollama 0.40.0 на MacBook Pro M2 Pro 16 ГБ в локальной сети, модель `qwen3.5:9b-nvfp4`. Три запроса: версия сервера, OpenAI-совместимый `/v1/chat/completions` и родной `/api/chat`.

Оба ответа модели — уверенные выдумки («тигровый барсук», «песец (белая сова)»; ирбис и леопард приняты за бренды электроники). Малая модель без источников — не справочник; поэтому в приложении локальная модель получает инструменты и базу знаний (задания 27–28).

```text
$ curl -s http://192.168.1.243:11434/api/version
{"version":"0.40.0"}

$ curl -s http://192.168.1.243:11434/v1/chat/completions -H "Content-Type: application/json" -d @c1.json
{"model":"qwen3.5:9b-nvfp4","reasoning_effort":"none","temperature":0,"messages":[{"role":"user","content":"Назови трёх хищников Евразии из семейства куньих. Только список."}]}
{"id":"chatcmpl-276","object":"chat.completion","created":1791383378,"model":"qwen3.5:9b-nvfp4","system_fingerprint":"fp_ollama","choices":[{"index":0,"message":{"role":"assistant","content":"1. Тигровый барсук\n2. Рыжий барсук\n3. Песец (белая сова)"},"finish_reason":"stop"}],"usage":{"prompt_tokens":32,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens":30,"total_tokens":62}}


$ curl -s http://192.168.1.243:11434/api/chat -d @c2.json   # родной API Ollama
{"model":"qwen3.5:9b-nvfp4","think":false,"stream":false,"options":{"temperature":0},"messages":[{"role":"user","content":"Чем ирбис отличается от леопарда? Три коротких пункта."}]}
{"model":"qwen3.5:9b-nvfp4","created_at":"2026-10-07T14:29:45.366296Z","message":{"role":"assistant","content":"Вот три основных отличия между брендами **Irbis** и **Leopard**:\n\n1.  **Качество сборки и материалы**: Leopard позиционируется как бренд среднего ценового сегмента с более качественными корпусами, экранами и комплектующими, тогда как Irbis часто использует более дешевые материалы и упрощенные конструкции для минимизации затрат.\n2.  **Надежность и сервис**: У Leopard обычно выше процент брака в массовых партиях и лучше развита сеть официальных сервисных центров по сравнению с Irbis, который известен частыми проблемами с гарантийным обслуживанием.\n3.  **Функциональность**: Модели Leopard часто оснащаются более современными процессорами, качественной камерой и актуальными версиями Android «из коробки», в то время как Irbis может предлагать устаревшие характеристики даже за схожие деньги."},"done":true,"done_reason":"stop","total_duration":6631628500,"load_duration":26850709,"prompt_eval_count":30,"prompt_eval_cached_count":3,"prompt_eval_duration":203427333,"eval_count":195,"eval_duration":6377646166}
```
