package com.vide2d.client.app;

import android.app.Activity;
import android.os.Bundle;

import java.io.File;

import com.vide2d.client.mobile.EbitenView;
import com.vide2d.client.mobile.Mobile;

/** Hosts the Ebitengine view; the Go side runs the ViDe2D client shell. */
public class MainActivity extends Activity {
    private EbitenView view;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        // Games live in the app's private folder: <files>/games/<game>/project.vide.json
        File games = new File(getFilesDir(), "games");
        games.mkdirs();
        Mobile.setGamesDir(games.getAbsolutePath());
        view = new EbitenView(this);
        setContentView(view);
    }

    @Override
    protected void onPause() {
        super.onPause();
        view.suspendGame();
    }

    @Override
    protected void onResume() {
        super.onResume();
        view.resumeGame();
    }
}
