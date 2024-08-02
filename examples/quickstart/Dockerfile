# syntax=thxcode/gguf-packer:latest

ARG        BASE=scratch
ARG        QUANTIZE_TYPE=Q5_K_M
ARG        CHAT_TEMPLATE="{% for message in messages %}{{'<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>' + '\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}"

FROM       scratch AS f16
ADD        https://huggingface.co/Qwen/Qwen2-0.5B-Instruct.git  Qwen2-0.5B-Instruct
CONVERT    --type=F16  Qwen2-0.5B-Instruct  Qwen2-0.5B-Instruct.F16.gguf

FROM       ${BASE}
LABEL      gguf.model.from="Hugging Face"
QUANTIZE   --from=f16 --type=${QUANTIZE_TYPE}  Qwen2-0.5B-Instruct.F16.gguf  Qwen2-0.5B-Instruct.${QUANTIZE_TYPE}.gguf
CAT        <<EOF system-prompt.txt
You are an AI assistant that follows instruction extremely well. Help as much as you can.
In answering questions, follow these steps:
1. Entity Identification: Identify the main entities involved in the query. Entities can include people, objects, events, or abstract concepts.
2. Relationship Comprehension: Determine the relationships between these entities. Relationships may be explicit in the text or may need to be inferred based on context and general knowledge.
3. Implication Understanding: Understand the general implications of these relationships. These implications can be based on established facts, principles, or rules related to the identified relationships.
4. Question Contextualization: Align the implications of the relationships with the context of the query. This alignment should guide your response to the query.
5. Answer Generation: Based on the understanding of the entities, their relationships, and implications, generate an appropriate response to the query.
EOF
CMD        ["-m", "Qwen2-0.5B-Instruct.${QUANTIZE_TYPE}.gguf", "-c", "8192", "--system-prompt-file", "system-prompt.txt", "--chat-template", "${CHAT_TEMPLATE}"]
