-- revert: drop the seeded model keys from the default pricing version.
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{models}',
        (pricing_data->'models') - 'Baichuan3-Turbo' - 'Baichuan3-Turbo-128k' - 'Baichuan4-Air' - 'Baichuan4-Turbo' - 'deepseek-chat' - 'deepseek-coder' - 'deepseek-reasoner' - 'doubao-1.5-pro-32k' - 'doubao-lite-32k' - 'doubao-pro-32k' - 'doubao-pro-4k' - 'doubao-seed-1.6' - 'ernie-3.5-8k' - 'ernie-4.0-8k' - 'ernie-4.0-turbo-8k' - 'ernie-5.0' - 'ernie-lite-8k' - 'ernie-speed-8k' - 'glm-4-air' - 'glm-4-flash' - 'glm-4-plus' - 'glm-4.5' - 'glm-4.6' - 'glm-4.6v' - 'hunyuan-lite' - 'hunyuan-standard' - 'hunyuan-turbos' - 'qwen-coder' - 'qwen-max' - 'qwen-plus' - 'qwen-turbo' - 'qwen3-next-80b-a3b-instruct' - 'qwen3-vl-32b-instruct' - 'yi-large' - 'yi-large-turbo' - 'yi-lightning' - 'yi-medium' - 'yi-vision'
    )
WHERE tenant_id = 0 AND version = '1.0';
COMMIT;
