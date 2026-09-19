-- 下游视频模型改名：grok-imagine-video-1.5-preview -> grok-imagine-video-1.5，
-- kling-video-v3-omni -> kling-v3-omni。下游只暴露网关自定义名，上游模型名由
-- mikuapi 适配器映射（grok-imagine-video-1.5-preview / kling-video-v3-omni），
-- 因此需要同步改写所有保存了下游模型名的位置。语句全部幂等：改名完成后再次
-- 执行不会命中任何行。

-- 1) 账号模型白名单：键（下游模型名）必须跟随改名；值仅在其等于旧下游名时
--    改为新下游名（保持恒等映射）。运营自定义的其它取值保持原样。
UPDATE accounts
SET credentials = jsonb_set(
        COALESCE(credentials, '{}'::jsonb),
        '{model_mapping}',
        (
            -- model_mapping 的值恒为字符串：必须用 jsonb_each_text 取 text，
            -- 否则 CASE 分支的字符串字面量会被当 JSON 解析而报
            -- "invalid input syntax for type json"。
            SELECT COALESCE(jsonb_object_agg(
                       CASE k
                           WHEN 'grok-imagine-video-1.5-preview' THEN 'grok-imagine-video-1.5'
                           WHEN 'kling-video-v3-omni' THEN 'kling-v3-omni'
                           ELSE k
                       END,
                       CASE v
                           WHEN 'grok-imagine-video-1.5-preview' THEN 'grok-imagine-video-1.5'
                           WHEN 'kling-video-v3-omni' THEN 'kling-v3-omni'
                           ELSE v
                       END
                   ), '{}'::jsonb)
            FROM jsonb_each_text(credentials->'model_mapping') AS t(k, v)
        ),
        TRUE
    ),
    updated_at = NOW()
WHERE platform = 'video'
  AND jsonb_typeof(COALESCE(credentials->'model_mapping', '{}'::jsonb)) = 'object'
  AND (credentials->'model_mapping' ?| ARRAY[
          'grok-imagine-video-1.5-preview',
          'kling-video-v3-omni'
      ]
      OR EXISTS (
          SELECT 1
          FROM jsonb_each_text(credentials->'model_mapping') AS t(k, v)
          WHERE v IN ('grok-imagine-video-1.5-preview', 'kling-video-v3-omni')
      ));

-- 2) 账号分辨率白名单：模型键跟随改名。
UPDATE accounts
SET extra = jsonb_set(
        COALESCE(extra, '{}'::jsonb),
        '{video_model_resolutions}',
        (
            SELECT COALESCE(jsonb_object_agg(
                       CASE k
                           WHEN 'grok-imagine-video-1.5-preview' THEN 'grok-imagine-video-1.5'
                           WHEN 'kling-video-v3-omni' THEN 'kling-v3-omni'
                           ELSE k
                       END,
                       v
                   ), '{}'::jsonb)
            FROM jsonb_each(extra->'video_model_resolutions') AS t(k, v)
        ),
        TRUE
    ),
    updated_at = NOW()
WHERE platform = 'video'
  AND jsonb_typeof(COALESCE(extra->'video_model_resolutions', '{}'::jsonb)) = 'object'
  AND extra->'video_model_resolutions' ?| ARRAY[
          'grok-imagine-video-1.5-preview',
          'kling-video-v3-omni'
      ];

-- 3) 账号时长白名单：模型键跟随改名。
UPDATE accounts
SET extra = jsonb_set(
        COALESCE(extra, '{}'::jsonb),
        '{video_model_durations}',
        (
            SELECT COALESCE(jsonb_object_agg(
                       CASE k
                           WHEN 'grok-imagine-video-1.5-preview' THEN 'grok-imagine-video-1.5'
                           WHEN 'kling-video-v3-omni' THEN 'kling-v3-omni'
                           ELSE k
                       END,
                       v
                   ), '{}'::jsonb)
            FROM jsonb_each(extra->'video_model_durations') AS t(k, v)
        ),
        TRUE
    ),
    updated_at = NOW()
WHERE platform = 'video'
  AND jsonb_typeof(COALESCE(extra->'video_model_durations', '{}'::jsonb)) = 'object'
  AND extra->'video_model_durations' ?| ARRAY[
          'grok-imagine-video-1.5-preview',
          'kling-video-v3-omni'
      ];

-- 4) 分组视频计费规则：model_code 跟随改名。
UPDATE video_group_pricing_rules
SET model_code = 'grok-imagine-video-1.5'
WHERE model_code = 'grok-imagine-video-1.5-preview';

UPDATE video_group_pricing_rules
SET model_code = 'kling-v3-omni'
WHERE model_code = 'kling-video-v3-omni';

-- 5) Agent 分组模型计费：model_code 跟随改名（仅视频媒体类型）。
-- 该表只在使用了 Agent 模型计费的部署中存在，必须做存在性守卫。
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public'
          AND table_name = 'agent_model_pricing'
    ) THEN
        UPDATE agent_model_pricing
        SET model_code = 'grok-imagine-video-1.5'
        WHERE media_type = 'video'
          AND model_code = 'grok-imagine-video-1.5-preview';

        UPDATE agent_model_pricing
        SET model_code = 'kling-v3-omni'
        WHERE media_type = 'video'
          AND model_code = 'kling-video-v3-omni';
    END IF;
END
$$;

-- 6) 分组模型白名单（{"enabled":bool,"models":[...]}）：models 数组元素跟随改名。
UPDATE groups
SET model_allowlist = jsonb_set(
        model_allowlist,
        '{models}',
        (
            SELECT COALESCE(jsonb_agg(
               CASE e
                   WHEN 'grok-imagine-video-1.5-preview' THEN 'grok-imagine-video-1.5'
                   WHEN 'kling-video-v3-omni' THEN 'kling-v3-omni'
                   ELSE e
               END
           ), '[]'::jsonb)
            FROM jsonb_array_elements_text(model_allowlist->'models') AS a(e)
        ),
        TRUE
    ),
    updated_at = NOW()
WHERE jsonb_typeof(COALESCE(model_allowlist->'models', '[]'::jsonb)) = 'array'
  AND model_allowlist->'models' ?| ARRAY[
          'grok-imagine-video-1.5-preview',
          'kling-video-v3-omni'
      ];
