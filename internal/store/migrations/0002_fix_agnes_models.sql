-- Agnes 图像模型实测只有 2.0/2.1/2.5 flash；3.0 是聊天模型，不能用于生图。
DELETE FROM models WHERE provider_id = 'agnes' AND model_id = 'agnes-image-3.0-flash';

INSERT OR IGNORE INTO models (provider_id, model_id, display_name) VALUES
    ('agnes', 'agnes-image-2.5-flash', 'Agnes Image 2.5 Flash'),
    ('agnes', 'agnes-image-2.1-flash', 'Agnes Image 2.1 Flash'),
    ('agnes', 'agnes-image-2.0-flash', 'Agnes Image 2.0 Flash');
